package smbios

// Device is one entry of the mac_devices table in mac_model_data.py.
// An empty DiscreteGPU stands for Python's None.
type Device struct {
	Name                 string
	CPU                  string
	CPUGeneration        string
	DiscreteGPU          string
	InitialSupport       string
	LastSupportedVersion string
}

// Devices returns a copy of the mac_devices table, in the original order.
func Devices() []Device {
	out := make([]Device, len(devices))
	copy(out, devices)
	return out
}

// DeviceByName mirrors get_mac_device_by_name. It returns false when no model
// has that name.
func DeviceByName(name string) (Device, bool) {
	for _, d := range devices {
		if d.Name == name {
			return d, true
		}
	}
	return Device{}, false
}
