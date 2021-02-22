package console

import (
	"fmt"
	"net"
	"os/exec"
	"strings"
	"sync"

	"github.com/jroimartin/gocui"
	"github.com/rancher/k3os/pkg/config"
	"github.com/sirupsen/logrus"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netlink/nl"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/apimachinery/pkg/util/validation"

	cfg "github.com/rancher/harvester-installer/pkg/config"
	"github.com/rancher/harvester-installer/pkg/util"
	"github.com/rancher/harvester-installer/pkg/widgets"
)

type InstallData struct {
	InstallMode string
	Device      string
	ServerURL   string
	Token       string
	Password    string
	SSHKeyURL   string
	Interface   string
	NetworkMode string
	HostName    string
	Address     string
	IP          string
	NetMask     string
	Gateway     string
	DNSServers  string
	Proxy       string
	ConfigURL   string
}

var (
	once        sync.Once
	installData = InstallData{}
)

func (c *Console) layoutInstall(g *gocui.Gui) error {
	var err error
	once.Do(func() {
		setPanels(c)
		initElements := []string{
			titlePanel,
			validatorPanel,
			notePanel,
			footerPanel,
			askCreatePanel,
		}
		var e widgets.Element
		for _, name := range initElements {
			e, err = c.GetElement(name)
			if err != nil {
				return
			}
			if err = e.Show(); err != nil {
				return
			}
		}
	})
	return err
}

func setPanels(c *Console) error {
	funcs := []func(*Console) error{
		addTitlePanel,
		addValidatorPanel,
		addNotePanel,
		addFooterPanel,
		addDiskPanel,
		addAskCreatePanel,
		addServerURLPanel,
		addPasswordPanels,
		addSSHKeyPanel,
		addNetworkPanel,
		addNetworkOptionPanel,
		addTokenPanel,
		addProxyPanel,
		addCloudInitPanel,
		addConfirmPanel,
		addInstallPanel,
		addSpinnerPanel,
	}
	for _, f := range funcs {
		if err := f(c); err != nil {
			return err
		}
	}
	return nil
}

func addTitlePanel(c *Console) error {
	maxX, maxY := c.Gui.Size()
	titleV := widgets.NewPanel(c.Gui, titlePanel)
	titleV.SetLocation(maxX/4, maxY/4-3, maxX/4*3, maxY/4)
	titleV.Focus = false
	c.AddElement(titlePanel, titleV)
	return nil
}

func addValidatorPanel(c *Console) error {
	maxX, maxY := c.Gui.Size()
	validatorV := widgets.NewPanel(c.Gui, validatorPanel)
	validatorV.SetLocation(maxX/4, maxY/4+5, maxX/4*3, maxY/4+7)
	validatorV.FgColor = gocui.ColorRed
	validatorV.Focus = false
	c.AddElement(validatorPanel, validatorV)
	return nil
}

func addNotePanel(c *Console) error {
	maxX, maxY := c.Gui.Size()
	noteV := widgets.NewPanel(c.Gui, notePanel)
	noteV.SetLocation(maxX/4, maxY/4+3, maxX, maxY/4+5)
	noteV.Wrap = true
	noteV.Focus = false
	c.AddElement(notePanel, noteV)
	return nil
}

func addFooterPanel(c *Console) error {
	maxX, maxY := c.Gui.Size()
	footerV := widgets.NewPanel(c.Gui, footerPanel)
	footerV.SetLocation(0, maxY-2, maxX, maxY)
	footerV.Focus = false
	c.AddElement(footerPanel, footerV)
	return nil
}

func addDiskPanel(c *Console) error {
	diskV, err := widgets.NewSelect(c.Gui, diskPanel, "", getDiskOptions)
	if err != nil {
		return err
	}
	diskV.KeyBindings = map[gocui.Key]func(*gocui.Gui, *gocui.View) error{
		gocui.KeyEnter: func(g *gocui.Gui, v *gocui.View) error {
			device, err := diskV.GetData()
			if err != nil {
				return err
			}
			installData.Device = device
			cfg.Config.K3OS.Install = &config.Install{
				Device: device,
			}
			diskV.Close()
			if cfg.Config.InstallMode == modeCreate {
				return showNext(c, tokenPanel)
			}
			return showNext(c, serverURLPanel)
		},
		gocui.KeyEsc: func(g *gocui.Gui, v *gocui.View) error {
			diskV.Close()
			return showNext(c, askCreatePanel)
		},
	}
	diskV.PreShow = func() error {
		diskV.DefaultValue = installData.Device
		return c.setContentByName(titlePanel, "Choose installation target. Device will be formatted")
	}
	c.AddElement(diskPanel, diskV)
	return nil
}

func getDiskOptions() ([]widgets.Option, error) {
	output, err := exec.Command("/bin/sh", "-c", `lsblk -r -o NAME,SIZE,TYPE | grep -w disk|cut -d ' ' -f 1,2`).CombinedOutput()
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSuffix(string(output), "\n"), "\n")
	var options []widgets.Option
	for _, line := range lines {
		splits := strings.SplitN(line, " ", 2)
		if len(splits) == 2 {
			options = append(options, widgets.Option{
				Value: "/dev/" + splits[0],
				Text:  line,
			})
		}
	}

	return options, nil
}

func addAskCreatePanel(c *Console) error {
	askOptionsFunc := func() ([]widgets.Option, error) {
		return []widgets.Option{
			{
				Value: modeCreate,
				Text:  "Create a new Harvester cluster",
			}, {
				Value: modeJoin,
				Text:  "Join an existing Harvester cluster",
			},
		}, nil
	}
	// new cluster or join existing cluster
	askCreateV, err := widgets.NewSelect(c.Gui, askCreatePanel, "", askOptionsFunc)
	if err != nil {
		return err
	}
	askCreateV.PreShow = func() error {
		if err := c.setContentByName(footerPanel, ""); err != nil {
			return err
		}
		askCreateV.DefaultValue = installData.InstallMode
		return c.setContentByName(titlePanel, "Choose installation mode")
	}
	askCreateV.PostClose = func() error {
		return c.setContentByName(footerPanel, "<Use ESC to go back to previous section>")
	}
	askCreateV.KeyBindings = map[gocui.Key]func(*gocui.Gui, *gocui.View) error{
		gocui.KeyEnter: func(g *gocui.Gui, v *gocui.View) error {
			selected, err := askCreateV.GetData()
			if err != nil {
				return err
			}
			askCreateV.Close()
			cfg.Config.InstallMode = selected
			installData.InstallMode = selected
			return showNext(c, diskPanel)
		},
	}
	c.AddElement(askCreatePanel, askCreateV)
	return nil
}

func addServerURLPanel(c *Console) error {
	serverURLV, err := widgets.NewInput(c.Gui, serverURLPanel, "Management address", false)
	if err != nil {
		return err
	}
	serverURLV.PreShow = func() error {
		c.Gui.Cursor = true
		serverURLV.DefaultValue = installData.ServerURL
		if err := c.setContentByName(titlePanel, "Configure management address"); err != nil {
			return err
		}
		return c.setContentByName(notePanel, serverURLNote)
	}
	serverURLV.KeyBindings = map[gocui.Key]func(*gocui.Gui, *gocui.View) error{
		gocui.KeyEnter: func(g *gocui.Gui, v *gocui.View) error {
			serverURL, err := serverURLV.GetData()
			if err != nil {
				return err
			}
			if serverURL == "" {
				return c.setContentByName(validatorPanel, "Management address is required")
			}

			// focus on task panel to prevent input
			asyncTaskV, err := c.GetElement(spinnerPanel)
			if err != nil {
				return err
			}
			asyncTaskV.Close()
			asyncTaskV.Show()

			fmtServerURL := getFormattedServerURL(serverURL)
			pingServerURL := fmtServerURL + "/ping"
			spinner := NewSpinner(c.Gui, spinnerPanel, fmt.Sprintf("Checking %q...", pingServerURL))
			spinner.Start()
			go func(g *gocui.Gui) {
				err := validateInsecureURL(pingServerURL)
				if err != nil {
					spinner.Stop(true, err.Error())
					g.Update(func(g *gocui.Gui) error {
						return showNext(c, serverURLPanel)
					})
					return
				}
				spinner.Stop(false, "")
				cfg.Config.K3OS.ServerURL = fmtServerURL
				installData.ServerURL = serverURL
				g.Update(func(g *gocui.Gui) error {
					return showNext(c, tokenPanel)
				})
			}(c.Gui)
			return nil
		},
		gocui.KeyEsc: func(g *gocui.Gui, v *gocui.View) error {
			g.Cursor = false
			serverURLV.Close()
			return showNext(c, diskPanel)
		},
	}
	serverURLV.PostClose = func() error {
		asyncTaskV, err := c.GetElement(spinnerPanel)
		if err != nil {
			return err
		}
		return asyncTaskV.Close()
	}
	c.AddElement(serverURLPanel, serverURLV)
	return nil
}

func addPasswordPanels(c *Console) error {
	maxX, maxY := c.Gui.Size()
	passwordV, err := widgets.NewInput(c.Gui, passwordPanel, "Password", true)
	if err != nil {
		return err
	}
	passwordConfirmV, err := widgets.NewInput(c.Gui, passwordConfirmPanel, "Confirm password", true)
	if err != nil {
		return err
	}

	passwordV.PreShow = func() error {
		passwordV.DefaultValue = installData.Password
		return nil
	}

	passwordV.KeyBindings = map[gocui.Key]func(*gocui.Gui, *gocui.View) error{
		gocui.KeyEnter: func(g *gocui.Gui, v *gocui.View) error {
			return showNext(c, passwordConfirmPanel)
		},
		gocui.KeyArrowDown: func(g *gocui.Gui, v *gocui.View) error {
			return showNext(c, passwordConfirmPanel)
		},
		gocui.KeyEsc: func(g *gocui.Gui, v *gocui.View) error {
			passwordV.Close()
			passwordConfirmV.Close()
			if err := c.setContentByName(notePanel, ""); err != nil {
				return err
			}
			return showNext(c, tokenPanel)
		},
	}
	passwordV.SetLocation(maxX/4, maxY/4, maxX/4*3, maxY/4+2)
	c.AddElement(passwordPanel, passwordV)

	passwordConfirmV.PreShow = func() error {
		c.Gui.Cursor = true
		passwordConfirmV.DefaultValue = installData.Password
		c.setContentByName(notePanel, "")
		return c.setContentByName(titlePanel, "Configure the password to access the node")
	}
	passwordConfirmV.KeyBindings = map[gocui.Key]func(*gocui.Gui, *gocui.View) error{
		gocui.KeyArrowUp: func(g *gocui.Gui, v *gocui.View) error {
			return showNext(c, passwordPanel)
		},
		gocui.KeyEnter: func(g *gocui.Gui, v *gocui.View) error {
			password1V, err := c.GetElement(passwordPanel)
			if err != nil {
				return err
			}
			password1, err := password1V.GetData()
			if err != nil {
				return err
			}
			password2, err := passwordConfirmV.GetData()
			if err != nil {
				return err
			}
			if password1 != password2 {
				return c.setContentByName(validatorPanel, "Password mismatching")
			}
			if password1 == "" {
				return c.setContentByName(validatorPanel, "Password is required")
			}
			password1V.Close()
			passwordConfirmV.Close()
			installData.Password = password1
			encrpyted, err := util.GetEncrptedPasswd(password1)
			if err != nil {
				return err
			}
			cfg.Config.K3OS.Password = encrpyted
			return showNext(c, sshKeyPanel)
		},
		gocui.KeyEsc: func(g *gocui.Gui, v *gocui.View) error {
			passwordV.Close()
			passwordConfirmV.Close()
			if err := c.setContentByName(notePanel, ""); err != nil {
				return err
			}
			return showNext(c, tokenPanel)
		},
	}
	passwordConfirmV.SetLocation(maxX/4, maxY/4+3, maxX/4*3, maxY/4+5)
	c.AddElement(passwordConfirmPanel, passwordConfirmV)

	return nil
}

func addSSHKeyPanel(c *Console) error {
	sshKeyV, err := widgets.NewInput(c.Gui, sshKeyPanel, "HTTP URL", false)
	if err != nil {
		return err
	}
	sshKeyV.PreShow = func() error {
		c.Gui.Cursor = true
		sshKeyV.DefaultValue = installData.SSHKeyURL
		if err := c.setContentByName(titlePanel, "Optional: import SSH keys"); err != nil {
			return err
		}
		return c.setContentByName(notePanel, "For example: https://github.com/<username>.keys")
	}
	sshKeyV.KeyBindings = map[gocui.Key]func(*gocui.Gui, *gocui.View) error{
		gocui.KeyEnter: func(g *gocui.Gui, v *gocui.View) error {
			url, err := sshKeyV.GetData()
			if err != nil {
				return err
			}
			cfg.Config.SSHAuthorizedKeys = []string{}
			if url != "" {
				// focus on task panel to prevent ssh input
				asyncTaskV, err := c.GetElement(spinnerPanel)
				if err != nil {
					return err
				}
				asyncTaskV.Close()
				asyncTaskV.Show()

				spinner := NewSpinner(c.Gui, spinnerPanel, fmt.Sprintf("Checking %q...", url))
				spinner.Start()

				go func(g *gocui.Gui) {
					pubKeys, err := getRemoteSSHKeys(url)
					if err != nil {
						spinner.Stop(true, err.Error())
						g.Update(func(g *gocui.Gui) error {
							return showNext(c, sshKeyPanel)
						})
						return
					}
					spinner.Stop(false, "")
					logrus.Debug("SSH public keys: ", pubKeys)
					cfg.Config.SSHAuthorizedKeys = pubKeys
					installData.SSHKeyURL = url
					g.Update(func(g *gocui.Gui) error {
						sshKeyV.Close()
						return showNext(c, networkPanel)
					})
				}(c.Gui)
				return nil
			}
			sshKeyV.Close()
			return showNext(c, networkPanel)
		},
		gocui.KeyEsc: func(g *gocui.Gui, v *gocui.View) error {
			sshKeyV.Close()
			return showNext(c, passwordConfirmPanel, passwordPanel)
		},
	}
	sshKeyV.PostClose = func() error {
		if err := c.setContentByName(notePanel, ""); err != nil {
			return err
		}
		asyncTaskV, err := c.GetElement(spinnerPanel)
		if err != nil {
			return err
		}
		return asyncTaskV.Close()
	}
	c.AddElement(sshKeyPanel, sshKeyV)
	return nil
}

func addTokenPanel(c *Console) error {
	tokenV, err := widgets.NewInput(c.Gui, tokenPanel, "Cluster token", false)
	if err != nil {
		return err
	}
	tokenV.PreShow = func() error {
		c.Gui.Cursor = true
		tokenV.DefaultValue = installData.Token
		if cfg.Config.InstallMode == modeCreate {
			if err := c.setContentByName(notePanel, clusterTokenNote); err != nil {
				return err
			}
		}
		return c.setContentByName(titlePanel, "Configure cluster token")
	}
	tokenV.KeyBindings = map[gocui.Key]func(*gocui.Gui, *gocui.View) error{
		gocui.KeyEnter: func(g *gocui.Gui, v *gocui.View) error {
			token, err := tokenV.GetData()
			if err != nil {
				return err
			}
			if token == "" {
				return c.setContentByName(validatorPanel, "Cluster token is required")
			}
			cfg.Config.K3OS.Token = token
			installData.Token = token
			tokenV.Close()
			return showNext(c, passwordConfirmPanel, passwordPanel)
		},
		gocui.KeyEsc: func(g *gocui.Gui, v *gocui.View) error {
			tokenV.Close()
			if cfg.Config.InstallMode == modeCreate {
				g.Cursor = false
				return showNext(c, diskPanel)
			}
			return showNext(c, serverURLPanel)
		},
	}
	c.AddElement(tokenPanel, tokenV)
	return nil
}

func addNetworkPanel(c *Console) error {
	networkV, err := widgets.NewSelect(c.Gui, networkPanel, "", getNetworkInterfaceOptions)
	if err != nil {
		return err
	}
	networkV.PreShow = func() error {
		c.Gui.Cursor = false
		networkV.DefaultValue = installData.Interface
		return c.setContentByName(titlePanel, "Select interface for the management network")
	}
	networkV.KeyBindings = map[gocui.Key]func(*gocui.Gui, *gocui.View) error{
		gocui.KeyEnter: func(g *gocui.Gui, v *gocui.View) error {
			selected, err := networkV.GetData()
			if err != nil {
				return err
			}
			selectedData := strings.Split(selected, ";")
			if len(selectedData) != 2 {
				return fmt.Errorf("failed to parser network view selected data: %s", selected)
			}
			installData.Interface, installData.Address = selectedData[0], selectedData[1]
			networkV.Close()
			if installData.Interface != "" {
				cfg.Config.ExtraK3sArgs = []string{"--flannel-iface", installData.Interface}
			}
			if installData.NetworkMode != networkModeStatic {
				return showNext(c, askNetworkModePanel, hostNamePanel)
			}
			return showNext(c, askNetworkModePanel, dnsServersPanel, gatewayPanel, addressPanel, hostNamePanel)
		},
		gocui.KeyEsc: func(g *gocui.Gui, v *gocui.View) error {
			networkV.Close()
			return showNext(c, sshKeyPanel)
		},
	}
	c.AddElement(networkPanel, networkV)
	return nil
}

func addNetworkOptionPanel(c *Console) error {
	maxX, maxY := c.Gui.Size()
	lastY := maxY / 4
	setLocation := func(p *widgets.Panel, high int) {
		var (
			x0 = maxX / 4
			y0 = lastY
			x1 = maxX / 4 * 3
			y1 = y0 + high
		)
		lastY += high
		p.SetLocation(x0, y0, x1, y1)
	}

	askOptionsFunc := func() ([]widgets.Option, error) {
		return []widgets.Option{
			{
				Value: networkModeDHCP,
				Text:  networkModeDHCP,
			}, {
				Value: networkModeStatic,
				Text:  networkModeStatic,
			},
		}, nil
	}
	askNetModeV, err := widgets.NewSelect(c.Gui, askNetworkModePanel, "", askOptionsFunc)
	if err != nil {
		return err
	}

	hostNameV, err := widgets.NewInput(c.Gui, hostNamePanel, "HostName", false)
	if err != nil {
		return err
	}

	addressV, err := widgets.NewInput(c.Gui, addressPanel, "IPv4 Address", false)
	if err != nil {
		return err
	}

	gatewayV, err := widgets.NewInput(c.Gui, gatewayPanel, "Gateway", false)
	if err != nil {
		return err
	}

	dnsServersV, err := widgets.NewInput(c.Gui, dnsServersPanel, "DNS Servers", false)
	if err != nil {
		return err
	}

	validatorV := widgets.NewPanel(c.Gui, networkValidatorPanel)

	closeAll := func() {
		askNetModeV.Close()
		hostNameV.Close()
		addressV.Close()
		gatewayV.Close()
		dnsServersV.Close()
		validatorV.Close()
	}
	goBack := func(g *gocui.Gui, v *gocui.View) error {
		closeAll()
		return showNext(c, networkPanel)
	}

	// askNetModeV
	askNetModeV.PreShow = func() error {
		c.Gui.Cursor = false
		askNetModeV.DefaultValue = installData.NetworkMode
		return c.setContentByName(titlePanel, "Choose network mode")
	}
	askNetModeV.KeyBindings = map[gocui.Key]func(*gocui.Gui, *gocui.View) error{
		gocui.KeyEnter: func(g *gocui.Gui, v *gocui.View) error {
			selected, err := askNetModeV.GetData()
			if err != nil {
				return err
			}
			closeAll()
			installData.NetworkMode = selected
			if selected != networkModeStatic {
				installData.Gateway = ""
				installData.NetMask = ""
				installData.DNSServers = ""
				cfg.Config.K3OS.DNSNameservers = nil
				cfg.Config.Runcmd = nil
				return showNext(c, askNetworkModePanel, hostNamePanel)
			}
			link, err := netlink.LinkByName(installData.Interface)
			if err != nil {
				return err
			}

			routes, err := netlink.RouteList(link, nl.FAMILY_V4)
			if err != nil {
				return err
			}
			for _, route := range routes {
				if route.Gw != nil {
					installData.Gateway = route.Gw.To4().String()
				}
			}
			return showNext(c, askNetworkModePanel, dnsServersPanel, gatewayPanel, addressPanel, hostNamePanel)
		},
		gocui.KeyEsc: goBack,
	}
	askNetModeV.PostClose = func() error {
		if installData.NetworkMode == "" {
			installData.NetworkMode = networkModeDHCP
		}
		return nil
	}
	setLocation(askNetModeV.Panel, 6)
	c.AddElement(askNetworkModePanel, askNetModeV)

	// hostNameV
	hostNameV.PreShow = func() error {
		c.Gui.Cursor = true
		if installData.HostName != "" {
			hostNameV.DefaultValue = installData.HostName
		}
		return c.setContentByName(titlePanel, "Configure HostName (FQDN)")
	}
	validateHostName := func() (string, error) {
		hostName, err := hostNameV.GetData()
		if err != nil {
			return "", err
		}
		if errs := validation.IsQualifiedName(hostName); len(errs) > 0 {
			return fmt.Sprintf("%s is not a valid hostname", hostName), nil
		}
		validatorV.Close()
		cfg.Config.Hostname = hostName
		installData.HostName = hostName
		return "", nil
	}
	hostNameV.KeyBindings = map[gocui.Key]func(*gocui.Gui, *gocui.View) error{
		gocui.KeyArrowUp: func(g *gocui.Gui, v *gocui.View) error {
			validateErrMsg, err := validateHostName()
			if err != nil {
				return err
			}
			if validateErrMsg != "" {
				return c.setContentByName(networkValidatorPanel, validateErrMsg)
			}
			askNetModeV.Close()
			return showNext(c, askNetworkModePanel)
		},
		gocui.KeyArrowDown: func(g *gocui.Gui, v *gocui.View) error {
			if installData.NetworkMode != networkModeStatic {
				return nil
			}

			validateErrMsg, err := validateHostName()
			if err != nil {
				return err
			}
			if validateErrMsg != "" {
				return c.setContentByName(networkValidatorPanel, validateErrMsg)
			}

			return showNext(c, addressPanel)
		},
		gocui.KeyEnter: func(g *gocui.Gui, v *gocui.View) error {
			validateErrMsg, err := validateHostName()
			if err != nil {
				return err
			}
			if validateErrMsg != "" {
				return c.setContentByName(networkValidatorPanel, validateErrMsg)
			}

			if installData.NetworkMode != networkModeStatic {
				closeAll()
				return showNext(c, proxyPanel)
			}
			return showNext(c, addressPanel)
		},
		gocui.KeyEsc: goBack,
	}
	hostNameV.DefaultValue = "harvester-" + rand.String(5)
	setLocation(hostNameV.Panel, 3)
	c.AddElement(hostNamePanel, hostNameV)

	// AddressV
	addressV.PreShow = func() error {
		c.Gui.Cursor = true
		addressV.DefaultValue = installData.Address
		return c.setContentByName(titlePanel, "Configure IPv4 Address (CIDR)")
	}
	validateAddress := func() (string, error) {
		address, err := addressV.GetData()
		if err != nil {
			return "", err
		}
		installData.Address = address
		ip, ipNet, err := net.ParseCIDR(installData.Address)
		if err != nil {
			return err.Error(), nil
		}
		validatorV.Close()
		mask := ipNet.Mask
		installData.IP = ip.String()
		installData.NetMask = fmt.Sprintf("%d.%d.%d.%d", mask[0], mask[1], mask[2], mask[3])
		return "", nil
	}
	addressVConfirm := func(g *gocui.Gui, v *gocui.View) error {
		validateErrMsg, err := validateAddress()
		if err != nil {
			return err
		}
		if validateErrMsg != "" {
			return c.setContentByName(networkValidatorPanel, validateErrMsg)
		}
		return showNext(c, gatewayPanel)
	}
	addressV.KeyBindings = map[gocui.Key]func(*gocui.Gui, *gocui.View) error{
		gocui.KeyArrowUp: func(g *gocui.Gui, v *gocui.View) error {
			validateErrMsg, err := validateAddress()
			if err != nil {
				return err
			}
			if validateErrMsg != "" {
				return c.setContentByName(networkValidatorPanel, validateErrMsg)
			}
			return showNext(c, hostNamePanel)
		},
		gocui.KeyArrowDown: addressVConfirm,
		gocui.KeyEnter:     addressVConfirm,
		gocui.KeyEsc:       goBack,
	}
	setLocation(addressV.Panel, 3)
	c.AddElement(addressPanel, addressV)

	// gatewayV
	gatewayV.PreShow = func() error {
		c.Gui.Cursor = true
		gatewayV.DefaultValue = installData.Gateway
		return c.setContentByName(titlePanel, "Configure Gateway")
	}
	validateGateway := func() (string, error) {
		gateway, err := gatewayV.GetData()
		if err != nil {
			return "", err
		}
		if err = validateIP(gateway); err != nil {
			return err.Error(), nil
		}
		validatorV.Close()
		installData.Gateway = gateway
		return "", nil
	}
	gatewayVConfirm := func(g *gocui.Gui, v *gocui.View) error {
		validateErrMsg, err := validateGateway()
		if err != nil {
			return err
		}
		if validateErrMsg != "" {
			return c.setContentByName(networkValidatorPanel, validateErrMsg)
		}
		return showNext(c, dnsServersPanel)
	}
	gatewayV.KeyBindings = map[gocui.Key]func(*gocui.Gui, *gocui.View) error{
		gocui.KeyArrowUp: func(g *gocui.Gui, v *gocui.View) error {
			validateErrMsg, err := validateGateway()
			if err != nil {
				return err
			}
			if validateErrMsg != "" {
				return c.setContentByName(networkValidatorPanel, validateErrMsg)
			}
			return showNext(c, addressPanel)
		},
		gocui.KeyArrowDown: gatewayVConfirm,
		gocui.KeyEnter:     gatewayVConfirm,
		gocui.KeyEsc:       goBack,
	}
	setLocation(gatewayV.Panel, 3)
	c.AddElement(gatewayPanel, gatewayV)

	// dnsServersV
	dnsServersV.PreShow = func() error {
		c.Gui.Cursor = true
		if installData.DNSServers != "" {
			dnsServersV.DefaultValue = installData.DNSServers
		}
		return c.setContentByName(titlePanel, "Configure DNS Servers")
	}
	validateDNSServers := func() (string, error) {
		dnsServers, err := dnsServersV.GetData()
		if err != nil {
			return "", err
		}
		dnsServerList := strings.Split(dnsServers, ",")
		for _, dnsServer := range dnsServerList {
			if err = validateIP(dnsServer); err != nil {
				return err.Error(), nil
			}
		}
		installData.DNSServers = dnsServers
		cfg.Config.K3OS.DNSNameservers = dnsServerList

		// run-cmd
		configureNetworkManual := fmt.Sprintf("/sbin/configure-network-manual %s %s %s %s %s",
			installData.Interface, installData.IP, installData.NetMask, installData.Gateway, strings.Join(dnsServerList, " "))
		cfg.Config.Runcmd = []string{configureNetworkManual}
		return "", nil
	}
	dnsServersV.KeyBindings = map[gocui.Key]func(*gocui.Gui, *gocui.View) error{
		gocui.KeyArrowUp: func(g *gocui.Gui, v *gocui.View) error {
			validateErrMsg, err := validateDNSServers()
			if err != nil {
				return err
			}
			if validateErrMsg != "" {
				return c.setContentByName(networkValidatorPanel, validateErrMsg)
			}
			return showNext(c, gatewayPanel)
		},
		gocui.KeyEnter: func(g *gocui.Gui, v *gocui.View) error {
			validateErrMsg, err := validateDNSServers()
			if err != nil {
				return err
			}
			if validateErrMsg != "" {
				return c.setContentByName(networkValidatorPanel, validateErrMsg)
			}
			closeAll()
			return showNext(c, proxyPanel)
		},
		gocui.KeyEsc: goBack,
	}
	setLocation(dnsServersV.Panel, 3)
	dnsServersV.DefaultValue = "8.8.8.8"
	c.AddElement(dnsServersPanel, dnsServersV)

	// validatorV
	validatorV.FgColor = gocui.ColorRed
	validatorV.Focus = false
	setLocation(validatorV, 3)
	c.AddElement(networkValidatorPanel, validatorV)

	return nil
}

func getNetworkInterfaceOptions() ([]widgets.Option, error) {
	var options = []widgets.Option{}
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	for _, i := range ifaces {
		if i.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := i.Addrs()
		if err != nil {
			return nil, err
		}
		var ips []string
		for _, addr := range addrs {
			if ipnet, ok := addr.(*net.IPNet); ok {
				if ipnet.IP.To4() != nil {
					ips = append(ips, ipnet.String())
				}
			}
		}
		option := widgets.Option{
			Value: i.Name,
			Text:  i.Name,
		}
		if len(ips) > 0 {
			allIP := strings.Join(ips, ",")
			option.Value = fmt.Sprintf("%s;%s", i.Name, allIP)
			option.Text = fmt.Sprintf("%s (%s)", i.Name, allIP)
		}
		options = append(options, option)
	}
	return options, nil
}

func addProxyPanel(c *Console) error {
	proxyV, err := widgets.NewInput(c.Gui, proxyPanel, "Proxy address", false)
	if err != nil {
		return err
	}
	proxyV.PreShow = func() error {
		c.Gui.Cursor = true
		proxyV.DefaultValue = installData.Proxy
		if err := c.setContentByName(titlePanel, "Optional: configure proxy"); err != nil {
			return err
		}
		return c.setContentByName(notePanel, proxyNote)
	}
	proxyV.KeyBindings = map[gocui.Key]func(*gocui.Gui, *gocui.View) error{
		gocui.KeyEnter: func(g *gocui.Gui, v *gocui.View) error {
			proxy, err := proxyV.GetData()
			if err != nil {
				return err
			}
			if proxy != "" {
				if cfg.Config.K3OS.Environment == nil {
					cfg.Config.K3OS.Environment = make(map[string]string)
				}
				cfg.Config.K3OS.Environment["http_proxy"] = proxy
				cfg.Config.K3OS.Environment["https_proxy"] = proxy
				installData.Proxy = proxy
			}
			proxyV.Close()
			noteV, err := c.GetElement(notePanel)
			if err != nil {
				return err
			}
			noteV.Close()
			return showNext(c, cloudInitPanel)
		},
		gocui.KeyEsc: func(g *gocui.Gui, v *gocui.View) error {
			proxyV.Close()
			noteV, err := c.GetElement(notePanel)
			if err != nil {
				return err
			}
			noteV.Close()
			if installData.NetworkMode != networkModeStatic {
				return showNext(c, askNetworkModePanel, hostNamePanel)
			}
			return showNext(c, askNetworkModePanel, hostNamePanel, addressPanel, gatewayPanel, dnsServersPanel)
		},
	}
	c.AddElement(proxyPanel, proxyV)
	return nil
}

func addCloudInitPanel(c *Console) error {
	cloudInitV, err := widgets.NewInput(c.Gui, cloudInitPanel, "HTTP URL", false)
	if err != nil {
		return err
	}
	cloudInitV.PreShow = func() error {
		cloudInitV.DefaultValue = installData.ConfigURL
		return c.setContentByName(titlePanel, "Optional: configure cloud-init")
	}
	cloudInitV.KeyBindings = map[gocui.Key]func(*gocui.Gui, *gocui.View) error{
		gocui.KeyEnter: func(g *gocui.Gui, v *gocui.View) error {
			configURL, err := cloudInitV.GetData()
			if err != nil {
				return err
			}
			confirmV, err := c.GetElement(confirmPanel)
			if err != nil {
				return err
			}
			cfg.Config.K3OS.Install.ConfigURL = configURL
			installData.ConfigURL = configURL
			cloudInitV.Close()
			installBytes, err := config.PrintInstall(cfg.Config.CloudConfig)
			if err != nil {
				return err
			}
			options := fmt.Sprintf("install mode: %v\n", cfg.Config.InstallMode)
			if cfg.Config.InstallMode == modeJoin {
				options += fmt.Sprintf("management address: %v\n", cfg.Config.K3OS.ServerURL)
			}
			if proxy, ok := cfg.Config.K3OS.Environment["http_proxy"]; ok {
				options += fmt.Sprintf("proxy address: %v\n", proxy)
			}
			if installData.SSHKeyURL != "" {
				options += fmt.Sprintf("ssh keys http url: %v\n", installData.SSHKeyURL)
			}
			options += fmt.Sprintf("management interface: %v\n", installData.Interface)
			options += fmt.Sprintf("network mode: %v\n", installData.NetworkMode)
			options += fmt.Sprintf("hostname: %v\n", cfg.Config.Hostname)
			if installData.NetworkMode == networkModeStatic {
				options += fmt.Sprintf("ipv4 address: %v\n", installData.Address)
				options += fmt.Sprintf("gateway: %v\n", installData.Gateway)
				options += fmt.Sprintf("dns servers: %v\n", installData.DNSServers)
			}
			options += string(installBytes)
			logrus.Debug("cfm cfg: ", fmt.Sprintf("%+v", cfg.Config.K3OS.Install))
			if cfg.Config.K3OS.Install != nil && !cfg.Config.K3OS.Install.Silent {
				confirmV.SetContent(options +
					"\nYour disk will be formatted and Harvester will be installed with \nthe above configuration. Continue?\n")
			}
			g.Cursor = false
			return showNext(c, confirmPanel)
		},
		gocui.KeyEsc: func(g *gocui.Gui, v *gocui.View) error {
			cloudInitV.Close()
			return showNext(c, proxyPanel)
		},
	}
	c.AddElement(cloudInitPanel, cloudInitV)
	return nil
}

func addConfirmPanel(c *Console) error {
	askOptionsFunc := func() ([]widgets.Option, error) {
		return []widgets.Option{
			{
				Value: "yes",
				Text:  "Yes",
			}, {
				Value: "no",
				Text:  "No",
			},
		}, nil
	}
	confirmV, err := widgets.NewSelect(c.Gui, confirmPanel, "", askOptionsFunc)
	if err != nil {
		return err
	}
	confirmV.PreShow = func() error {
		return c.setContentByName(titlePanel, "Confirm installation options")
	}
	confirmV.KeyBindings = map[gocui.Key]func(*gocui.Gui, *gocui.View) error{
		gocui.KeyEnter: func(g *gocui.Gui, v *gocui.View) error {
			confirmed, err := confirmV.GetData()
			if err != nil {
				return err
			}
			if confirmed == "no" {
				confirmV.Close()
				c.setContentByName(titlePanel, "")
				c.setContentByName(footerPanel, "")
				go util.SleepAndReboot()
				return c.setContentByName(notePanel, "Installation halted. Rebooting system in 5 seconds")
			}
			confirmV.Close()
			customizeConfig()
			return showNext(c, installPanel)
		},
		gocui.KeyEsc: func(g *gocui.Gui, v *gocui.View) error {
			confirmV.Close()
			return showNext(c, cloudInitPanel)
		},
	}
	c.AddElement(confirmPanel, confirmV)
	return nil
}

func addInstallPanel(c *Console) error {
	maxX, maxY := c.Gui.Size()
	installV := widgets.NewPanel(c.Gui, installPanel)
	installV.PreShow = func() error {
		go doInstall(c.Gui)
		return c.setContentByName(footerPanel, "")
	}
	installV.Title = " Installing Harvester "
	installV.SetLocation(maxX/8, maxY/8, maxX/8*7, maxY/8*7)
	c.AddElement(installPanel, installV)
	installV.Frame = true
	return nil
}

func addSpinnerPanel(c *Console) error {
	maxX, maxY := c.Gui.Size()
	asyncTaskV := widgets.NewPanel(c.Gui, spinnerPanel)
	asyncTaskV.SetLocation(maxX/4, maxY/4+7, maxX/4*3, maxY/4+9)
	c.AddElement(spinnerPanel, asyncTaskV)
	return nil
}
