package smbios

import "testing"

func TestDeviceByName(t *testing.T) {
	d, ok := DeviceByName("MacPro7,1")
	if !ok || d.CPU != "W-3245M" || d.LastSupportedVersion != "25.99.99" {
		t.Errorf("DeviceByName(MacPro7,1) = %+v, %v", d, ok)
	}
	if _, ok := DeviceByName("NotAMac1,1"); ok {
		t.Error("DeviceByName returned a device for an unknown name")
	}
}

func TestDevicesReturnsCopy(t *testing.T) {
	list := Devices()
	list[0].Name = "changed"
	if Devices()[0].Name == "changed" {
		t.Error("Devices() exposes the internal table")
	}
}
