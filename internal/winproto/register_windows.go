//go:build windows

package winproto

import "golang.org/x/sys/windows/registry"

func register(exe string) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, `Software\Classes\gamelink`, registry.SET_VALUE|registry.CREATE_SUB_KEY)
	if err != nil {
		return err
	}
	if err := k.SetStringValue("", "URL:GameLink Protocol"); err != nil {
		k.Close()
		return err
	}
	if err := k.SetStringValue("URL Protocol", ""); err != nil {
		k.Close()
		return err
	}
	k.Close()
	cmd, _, err := registry.CreateKey(registry.CURRENT_USER, `Software\Classes\gamelink\shell\open\command`, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer cmd.Close()
	return cmd.SetStringValue("", OpenCommand(exe))
}
