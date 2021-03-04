package console

import (
	"bufio"
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/jroimartin/gocui"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/net"

	"github.com/rancher/harvester-installer/pkg/util"
	"github.com/rancher/harvester-installer/pkg/version"
	"github.com/rancher/harvester-installer/pkg/widgets"
)

const (
	colorBlack = iota
	colorRed
	colorGreen
	colorYellow
	colorBlue

	statusRunning   = "Running"
	statusUnknown   = "Unknown"
	statusSettingUp = "Setting up Harvester"

	logo string = `
██╗░░██╗░█████╗░██████╗░██╗░░░██╗███████╗░██████╗████████╗███████╗██████╗░
██║░░██║██╔══██╗██╔══██╗██║░░░██║██╔════╝██╔════╝╚══██╔══╝██╔════╝██╔══██╗
███████║███████║█████╔╝╚██╗░░██╔╝█████╗░░╚█████╗░░░░██║░░░█████╗░░██████╔╝
██╔══██║██╔══██║██╔══██╗░╚████╔╝░██╔══╝░░░╚═══██╗░░░██║░░░██╔══╝░░██╔══██╗
██║░░██║██║░░██║██║░░██║░░╚██╔╝░░███████╗██████╔╝░░░██║░░░███████╗██║░░██║
╚═╝░░╚═╝╚═╝░░╚═╝╚═╝░░╚═╝░░░╚═╝░░░╚══════╝╚═════╝░░░░╚═╝░░░╚══════╝╚═╝░░╚═╝`
)

type state struct {
	installed bool
	firstHost bool
}

var (
	current state
)

func (c *Console) layoutDashboard(g *gocui.Gui) error {
	once.Do(func() {
		if err := initState(); err != nil {
			logrus.Error(err)
		}
		if err := g.SetKeybinding("", gocui.KeyF12, gocui.ModNone, toShell); err != nil {
			logrus.Error(err)
		}
		logrus.Infof("state: %+v", current)
	})
	maxX, maxY := g.Size()
	if v, err := g.SetView("url", maxX/2-40, 10, maxX/2+40, 14); err != nil {
		if err != gocui.ErrUnknownView {
			return err
		}
		v.Frame = false
		v.Wrap = true
		go syncManagementURL(context.Background(), g)
	}
	if v, err := g.SetView("status", maxX/2-40, 14, maxX/2+40, 18); err != nil {
		if err != gocui.ErrUnknownView {
			return err
		}
		v.Frame = false
		v.Wrap = true
		fmt.Fprintf(v, "Current status: ")
		go syncHarvesterStatus(context.Background(), g)
	}
	if v, err := g.SetView("footer", 0, maxY-2, maxX, maxY); err != nil {
		if err != gocui.ErrUnknownView {
			return err
		}
		v.Frame = false
		fmt.Fprintf(v, "<Use F12 to switch between Harvester console and Shell>")
	}
	if err := logoPanel(g); err != nil {
		return err
	}
	return nil
}

func logoPanel(g *gocui.Gui) error {
	maxX, _ := g.Size()
	if v, err := g.SetView("logo", maxX/2-40, 1, maxX/2+40, 9); err != nil {
		if err != gocui.ErrUnknownView {
			return err
		}
		v.Frame = false
		fmt.Fprintf(v, logo)
		versionStr := "version: " + version.Version
		logoLength := 74
		nSpace := logoLength - len(versionStr)
		fmt.Fprintf(v, "\n%*s", nSpace, "")
		fmt.Fprintf(v, "%s", versionStr)
	}
	return nil
}

func toShell(g *gocui.Gui, v *gocui.View) error {
	g.Cursor = true
	maxX, _ := g.Size()
	adminPasswordFrameV := widgets.NewPanel(g, "adminPasswordFrame")
	adminPasswordFrameV.Frame = true
	adminPasswordFrameV.SetLocation(maxX/2-35, 10, maxX/2+35, 17)
	if err := adminPasswordFrameV.Show(); err != nil {
		return err
	}
	adminPasswordV, err := widgets.NewInput(g, "adminPassword", "Input password: ", true)
	if err != nil {
		return err
	}
	adminPasswordV.SetLocation(maxX/2-30, 12, maxX/2+30, 14)
	validatorV := widgets.NewPanel(g, validatorPanel)
	validatorV.SetLocation(maxX/2-30, 14, maxX/2+30, 16)
	validatorV.FgColor = gocui.ColorRed
	validatorV.Focus = false

	adminPasswordV.KeyBindings = map[gocui.Key]func(*gocui.Gui, *gocui.View) error{
		gocui.KeyEnter: func(g *gocui.Gui, v *gocui.View) error {
			passwd, err := adminPasswordV.GetData()
			if err != nil {
				return err
			}
			if validateAdminPassword(passwd) {
				return gocui.ErrQuit
			}
			if err := validatorV.Show(); err != nil {
				return err
			}
			validatorV.SetContent("Invalid credential")
			return nil
		},
		gocui.KeyEsc: func(g *gocui.Gui, v *gocui.View) error {
			g.Cursor = false
			if err := adminPasswordFrameV.Close(); err != nil {
				return err
			}
			if err := adminPasswordV.Close(); err != nil {
				return err
			}
			return validatorV.Close()
		},
	}
	return adminPasswordV.Show()
}

func validateAdminPassword(passwd string) bool {
	file, err := os.Open("/etc/shadow")
	if err != nil {
		return false
	}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "rancher:") {
			if util.CompareByShadow(passwd, line) {
				return true
			}
			return false
		}
	}
	return false
}

func initState() error {
	envFile := "/etc/rancher/k3s/k3s-service.env"
	if _, err := os.Stat(envFile); os.IsNotExist(err) {
		return err
	}
	content, err := ioutil.ReadFile(envFile)
	if err != nil {
		return err
	}
	serverURL, err := getServerURLFromEnvData(content)
	if err != nil {
		return err
	}

	if serverURL != "" {
		os.Setenv("KUBECONFIG", "/var/lib/rancher/k3s/agent/kubelet.kubeconfig")
	} else {
		current.firstHost = true
	}

	return nil
}

func syncManagementURL(ctx context.Context, g *gocui.Gui) {
	// sync url at the beginning
	doSyncManagementURL(g)

	syncDuration := 5 * time.Second
	ticker := time.NewTicker(syncDuration)
	go func() {
		<-ctx.Done()
		ticker.Stop()
	}()
	for range ticker.C {
		doSyncManagementURL(g)
	}
}

func doSyncManagementURL(g *gocui.Gui) {
	managementIP := getFirstReadyMasterIP()
	if len(managementIP) == 0 {
		managementNIC := getManagementNIC()
		ip, err := net.ChooseBindAddressForInterface(managementNIC)
		if err != nil {
			logrus.Error(err)
			return
		}
		managementIP = ip.String()
	}
	managementURL := fmt.Sprintf("https://%s:%s", managementIP, harvesterNodePort)
	g.Update(func(g *gocui.Gui) error {
		v, err := g.View("url")
		if err != nil {
			return err
		}
		v.Clear()
		fmt.Fprintf(v, "Harvester management URL: \n\n%s", managementURL)
		return nil
	})
}

func getFirstReadyMasterIP() string {
	// get first ready master node's internal ip
	cmd := exec.Command("/bin/sh", "-c", `kubectl get no -l 'node-role.kubernetes.io/master=true' --sort-by='.metadata.creationTimestamp' \
-o jsonpath='{range .items[*]}{@.metadata.name}:{range @.status.conditions[*]}{@.type}={@.status};{end}{range @.status.addresses[*]}{@.type}={@.address};{end}{"\n"}{end}' 2>/dev/null \
| grep 'Ready=True' | head -n 1 | tr ';' '\n' | awk -F '=' '/InternalIP/{printf $2}'`)
	cmd.Env = os.Environ()
	output, err := cmd.Output()
	outStr := string(output)
	if err != nil {
		logrus.Error(err, outStr)
		return ""
	}
	return outStr
}

func getManagementNIC() string {
	cmd := exec.Command("/bin/sh", "-c", `yq eval '.k3os.k3sArgs[]' /k3os/system/config.yaml | sed -n '/--flannel-iface/ {n;p}'`)
	cmd.Env = os.Environ()
	output, err := cmd.Output()
	outStr := string(output)
	if err != nil {
		logrus.Error(err, outStr)
		return ""
	}
	return outStr
}

func syncHarvesterStatus(ctx context.Context, g *gocui.Gui) {
	// sync status at the beginning
	doSyncHarvesterStatus(g)

	syncDuration := 5 * time.Second
	ticker := time.NewTicker(syncDuration)
	go func() {
		<-ctx.Done()
		ticker.Stop()
	}()
	for range ticker.C {
		doSyncHarvesterStatus(g)
	}
}

func doSyncHarvesterStatus(g *gocui.Gui) {
	status := getHarvesterStatus()
	g.Update(func(g *gocui.Gui) error {
		v, err := g.View("status")
		if err != nil {
			return err
		}
		v.Clear()
		fmt.Fprintln(v, "Current status: \n\n"+status)
		return nil
	})
}

func k8sIsReady() bool {
	cmd := exec.Command("/bin/sh", "-c", `kubectl get no -o jsonpath='{.items[*].metadata.name}'`)
	cmd.Env = os.Environ()
	output, err := cmd.CombinedOutput()
	if err != nil {
		logrus.Error(err, string(output))
		return false
	}
	if string(output) == "" {
		//no node is added
		return false
	}
	return true
}

func chartIsInstalled() bool {
	cmd := exec.Command("/bin/sh", "-c", `kubectl -n kube-system get job helm-install-harvester -o jsonpath='{.status.succeeded}'`)
	cmd.Env = os.Environ()
	output, err := cmd.CombinedOutput()
	outStr := string(output)
	if err != nil {
		logrus.Error(err, outStr)
		return false
	}
	if len(outStr) == 0 {
		return false
	}
	succeeded, err := strconv.Atoi(outStr)
	if err != nil {
		logrus.Error(err, outStr)
		return false
	}

	return succeeded >= 1
}

func harvesterPodStatus() (string, error) {
	cmd := exec.Command("/bin/sh", "-c", `kubectl get po -n harvester-system -l "app.kubernetes.io/name=harvester" -o jsonpath='{.items[*].status.phase}' | tr ' ' '\n' | sort -u | xargs echo -n`)
	cmd.Env = os.Environ()
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", errors.Wrap(err, string(output))
	}
	outStr := string(output)
	if strings.Contains(outStr, statusRunning) {
		return statusRunning, nil
	}
	return outStr, nil
}

func nodeIsPresent() bool {
	hostname, err := os.Hostname()
	if err != nil {
		logrus.Errorf("failed to get hostname: %v", err)
		return false
	}

	kcmd := fmt.Sprintf("kubectl get no %s", hostname)
	cmd := exec.Command("/bin/sh", "-c", kcmd)
	cmd.Env = os.Environ()
	output, err := cmd.CombinedOutput()
	if err != nil {
		logrus.Error(err, string(output))
		return false
	}

	return true
}

func getHarvesterStatus() string {
	if current.firstHost && !current.installed {
		if !k8sIsReady() || !chartIsInstalled() {
			return statusSettingUp
		}
		current.installed = true
	}

	if !nodeIsPresent() {
		return wrapColor(statusUnknown, colorYellow)
	}

	status, err := harvesterPodStatus()
	if err != nil {
		status = wrapColor(err.Error(), colorRed)
	}
	if status == statusRunning {
		status = wrapColor(status, colorGreen)
	} else {
		status = wrapColor(status, colorYellow)
	}
	return status
}

func wrapColor(s string, color int) string {
	return fmt.Sprintf("\033[3%d;7m%s\033[0m", color, s)
}
