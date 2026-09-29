//go:build !windows || !amd64

package main

import "errors"

func nativeDesktopTargets() ([]DesktopTarget, error) {
	return nil, errors.New("桌面共享目前支持 Windows x64")
}
func nativeDesktopCapture(string) (desktopObservation, error) {
	return desktopObservation{}, errors.New("此平台不支持桌面共享")
}
func nativeDesktopInput(desktopObservation, DesktopAction) error {
	return errors.New("此平台不支持桌面控制")
}
