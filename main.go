package main

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	gosxnotifier "github.com/deckarep/gosx-notifier"
	"github.com/elazarl/goproxy"
	"github.com/getlantern/systray"
)

const (
	PORT       = "9123"
	PROXY_PORT = "9000"
	ON         = "▶︎"
	OFF        = "◼︎"
	NAME       = "RP"
)

func main() {
	reject := true
	proxy := goproxy.NewProxyHttpServer()
	rejectedHosts := []string{"developerservices2.apple.com"}
	if len(os.Args) > 1 {
		rejectedHosts = strings.Split(os.Args[1], ",")
	}
	rejectedHostsMatcher := regexp.MustCompile(strings.Join(rejectedHosts, "|"))

	systray.Run(func() {
		enabled := isEnabled()
		systray.SetTitle(title(enabled))
		enabledEntry := systray.AddMenuItemCheckbox("Enabled", "enabled", enabled)
		systray.AddSeparator()
		rejectEntry := systray.AddMenuItemCheckbox("Reject:", "reject", true)
		for _, entry := range rejectedHosts {
			systray.AddMenuItem(entry, entry).Disable()
		}
		systray.AddSeparator()
		quit := systray.AddMenuItem("Quit", "Quit")

		go func() {
			for {
				select {
				case <-time.After(1 * time.Second):
				}
				enabled = isEnabled()
				systray.SetTitle(title(enabled))
				if enabled {
					enabledEntry.Check()
				} else {
					enabledEntry.Uncheck()
				}
			}
		}()

		go func() {
			for {
				select {
				case <-quit.ClickedCh:
					systray.Quit()
				case <-enabledEntry.ClickedCh:
					if enabled {
						if err := setNetworkProxyStatus(false, "", ""); err == nil {
							enabledEntry.Uncheck()
						} else {
							notify("Error", "could not disable proxy: "+err.Error())
							fmt.Println(err)
						}
					} else {
						if err := setNetworkProxyStatus(true, "localhost", PORT); err == nil {
							enabledEntry.Check()
						} else {
							notify("Error", "could not enabe proxy: "+err.Error())
							fmt.Println(err)
						}
					}
				case <-rejectEntry.ClickedCh:
					if reject {
						proxy.Logger.Printf("Reject disabled")
						rejectEntry.Uncheck()
					} else {
						proxy.Logger.Printf("Reject enabled")
						rejectEntry.Check()
					}
					reject = !reject
				}
			}
		}()

		go func() {
			useProxy := isPortOpen(PROXY_PORT)
			proxy.Logger.Printf("Currently using proxy? %v", useProxy)
			proxy.Logger.Printf("Rejecting %v", rejectedHosts)
			lock := sync.RWMutex{}
			proxy.Tr.Proxy = func(r *http.Request) (*url.URL, error) {
				lock.RLock()
				defer lock.RUnlock()
				if !useProxy {
					return nil, nil
				}
				return url.Parse("http://localhost:" + PROXY_PORT)
			}
			proxyDialer := proxy.NewConnectDialToProxy("http://localhost:" + PROXY_PORT)
			go func() {
				for {
					lock.Lock()
					old := useProxy
					useProxy = isPortOpen(PROXY_PORT)
					proxy.ConnectDial = nil
					if useProxy {
						proxy.ConnectDial = proxyDialer
					}
					if old != useProxy {
						log.Printf("Proxy status changed from %v to %v", old, useProxy)
					}
					lock.Unlock()
					<-time.After(2 * time.Second)
				}
			}()
			proxy.OnRequest(goproxy.ReqHostMatches(rejectedHostsMatcher)).HandleConnectFunc(func(host string, ctx *goproxy.ProxyCtx) (*goproxy.ConnectAction, string) {
				if !reject {
					return goproxy.OkConnect, host
				}
				proxy.Logger.Printf("rejecting %s", host)
				return goproxy.RejectConnect, host
			})
			err := http.ListenAndServe("localhost:"+PORT, proxy)
			if err != nil {
				notify("ERROR", "could not start proxy: "+err.Error())
				panic(err)
			}
		}()
	}, nil)
}

func title(enabled bool) string {
	if enabled {
		return NAME + ON
	}
	return NAME + OFF
}

func notify(title, message string) {
	not := gosxnotifier.NewNotification(message)
	not.Title = title
	not.Push()
}

func isEnabled() bool {
	isEnabled, host, port, _ := getNetworkProxyStatus()
	return isEnabled && (host == "localhost" || host == "127.0.0.1") && port == PORT
}

func isPortOpen(port string) bool {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("localhost", port), 1*time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}
func getNetworkProxyStatus() (active bool, host string, port string, err error) {
	var out string
	out, err = cmd("networksetup -getsecurewebproxy wi-fi", false)
	if err != nil {
		return
	}
	lines := strings.Split(out, "\n")
	active = lines[0] == "Enabled: Yes"
	host = strings.Split(lines[1], ": ")[1]
	port = strings.Split(lines[2], ": ")[1]
	return
}

// networksetup -setsecurewebproxy <networkservice> <domain> <port number> <authenticated> <username> <password>
func setNetworkProxyStatus(active bool, host string, port string) (err error) {
	if active {
		_, err = cmd(fmt.Sprintf("networksetup -setsecurewebproxy wi-fi %s %s", host, port), true)
	} else {
		_, err = cmd(fmt.Sprintf("networksetup -setsecurewebproxystate wi-fi off"), true)
	}
	return
}

func canSudo() bool {
	out, err := exec.Command("sudo", "-l").CombinedOutput()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "(ALL) ALL") || strings.Contains(string(out), "(ALL : ALL) ALL")
}

func cmd(cmd string, sudo bool) (string, error) {
	sudoPrefix := ""
	if sudo {
		if !canSudo() {
			return "", fmt.Errorf("cannot sudo")
		}
		sudoPrefix = "sudo "
	}
	command := exec.Command("/bin/sh", "-c", sudoPrefix+cmd)
	data, err := command.CombinedOutput()
	if err != nil {
		fmt.Println(err)
		return "", err
	}
	return string(data), nil
}
