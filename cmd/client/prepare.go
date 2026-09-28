package main

import (
	"fmt"
	"os"

	"github.com/Q-xuan/GameLink/internal/winproto"
	"github.com/Q-xuan/GameLink/internal/wintunbin"
)

func prepareClient() error {
	if err := wintunbin.Install(); err != nil {
		return err
	}
	if err := winproto.RegisterCurrent(); err != nil {
		fmt.Fprintf(os.Stderr, "注册 gamelink:// 协议失败: %v\n", err)
	}
	return nil
}
