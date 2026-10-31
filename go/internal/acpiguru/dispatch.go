package acpiguru

import (
	"fmt"

	"github.com/Lufel3846/OpCore-Simplify/go/internal/plist"
)

// callPatch mirrors getattr(self, patch.function_name)() for the patches that
// return a dict (nil means Python returned None).
func (g *Guru) callPatch(functionName string) (*plist.Dict, error) {
	switch functionName {
	case "ambient_light_sensor":
		return g.AmbientLightSensor()
	case "fix_apic_processor_id":
		return g.FixAPICProcessorID()
	case "add_system_management_bus_device":
		return g.AddSystemManagementBusDevice()
	case "disable_unsupported_device":
		return g.DisableUnsupportedDevice()
	case "fake_embedded_controller":
		return g.FakeEmbeddedController()
	case "remove_conditional_scope":
		return g.RemoveConditionalScope()
	case "fix_hp_005_post_error":
		return g.FixHP005PostError()
	case "fix_irq_conflicts":
		return g.FixIRQConflicts()
	case "enable_gpio_device":
		return g.EnableGPIODevice()
	case "add_intel_management_engine":
		return g.AddIntelManagementEngine()
	case "add_memory_controller_device":
		return g.AddMemoryControllerDevice()
	case "enable_nvram_support":
		return g.EnableNVRAMSupport()
	case "drop_cpu_tables":
		return g.DropCPUTables()
	case "enable_cpu_power_management":
		return g.EnableCPUPowerManagement()
	case "enable_backlight_controls":
		return g.EnableBacklightControls()
	case "add_null_ethernet_device":
		return g.AddNullEthernetDevice()
	case "fix_system_clock_hedt":
		return g.FixSystemClockHEDT()
	case "fix_system_clock_awac":
		return g.FixSystemClockAWAC()
	case "instant_wake_fix":
		return g.InstantWakeFix()
	case "surface_laptop_special_patch":
		return g.SurfaceLaptopSpecialPatch()
	case "fix_uncore_bridge":
		return g.FixUncoreBridge()
	case "disable_usb_hub_devices":
		return g.DisableUSBHubDevices()
	case "add_usb_power_properties":
		return g.AddUSBPowerProperties()
	case "return_thermal_zone":
		return g.ReturnThermalZone()
	case "operating_system_patch":
		return g.OperatingSystemPatch()
	}
	return nil, fmt.Errorf("AttributeError: 'ACPIGuru' object has no attribute '%s'", functionName)
}

// ACPILoad is what the checked patches contribute to config["ACPI"].
type ACPILoad struct {
	Add, Delete, Patch []any
}

func extendList(dst []any, d *plist.Dict, key string) ([]any, error) {
	v, ok := d.Get(key)
	if !ok {
		return dst, nil
	}
	l, isList := v.([]any)
	if !isList {
		return nil, fmt.Errorf("TypeError: '%T' object is not iterable", v)
	}
	return append(dst, l...), nil
}

// RunPatches mirrors the loop of build_opencore_efi over the checked patches.
// setECEnabler is called with the result of the BATP check, which decides
// whether the ECEnabler kext is checked.
func (g *Guru) RunPatches(setECEnabler func(checked bool)) (*ACPILoad, error) {
	load := &ACPILoad{}
	for _, p := range g.Patches {
		if !p.Checked {
			continue
		}
		if p.Name == "BATP" {
			ok, err := g.BatteryStatusPatch()
			if err != nil {
				return nil, err
			}
			p.Checked = ok
			setECEnabler(ok)
			continue
		}
		d, err := g.callPatch(p.FunctionName)
		if err != nil {
			return nil, err
		}
		if d == nil {
			continue
		}
		if load.Add, err = extendList(load.Add, d, "Add"); err != nil {
			return nil, err
		}
		if load.Delete, err = extendList(load.Delete, d, "Delete"); err != nil {
			return nil, err
		}
		if load.Patch, err = extendList(load.Patch, d, "Patch"); err != nil {
			return nil, err
		}
	}
	return load, nil
}
