package config

import (
	"sort"

	"github.com/Lufel3846/OpCore-Simplify/go/internal/plist"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/pyrt"
)

// igpuContext holds what the integrated-GPU branches read from the report.
type igpuContext struct {
	out      *plist.Dict
	platform any
	gpuName  string
	monitor  any
	macosVer string
	deviceID string
}

func (c *igpuContext) set(k, v string) { c.out.Set(k, v) }

// connectedToGPU mirrors any(monitor["Connected GPU"] == gpu name), optionally
// skipping VGA connectors the way the Intel branches do.
func (c *igpuContext) connectedToGPU(excludeVGA bool) (bool, error) {
	return anyMonitor(c.monitor, func(_ string, info any) (bool, error) {
		if excludeVGA {
			ct, err := pyrt.Get(info, "Connector Type")
			if err != nil {
				return false, err
			}
			if pyrt.Equal(ct, "VGA") {
				return false, nil
			}
		}
		cg, err := pyrt.Get(info, "Connected GPU")
		if err != nil {
			return false, err
		}
		return pyrt.Equal(cg, c.gpuName), nil
	})
}

// notConnected mirrors "not any(...)" for connectedToGPU.
func (c *igpuContext) notConnected(excludeVGA bool) (bool, error) {
	any, err := c.connectedToGPU(excludeVGA)
	return !any, err
}

// resolutionAtLeastAny mirrors any(tuple(ref) <= tuple(monitor Resolution)).
func (c *igpuContext) resolutionAtLeastAny(reference string) (bool, error) {
	return anyMonitor(c.monitor, func(_ string, info any) (bool, error) {
		return resolutionAtLeast(info, reference)
	})
}

// igpuProperties mirrors Prodigy.igpu_properties. gpu is the report entry of the
// integrated GPU. The early returns of the original are kept: those results are
// not sorted and skip the final framebuffer adjustments.
func igpuProperties(platform any, gpuName string, gpu any, monitor any, macosVersion string) (*plist.Dict, error) {
	rawID, err := pyrt.Get(gpu, "Device ID")
	if err != nil {
		return nil, err
	}
	fullID, err := pyrt.Str(rawID)
	if err != nil {
		return nil, err
	}
	ctx := &igpuContext{
		out:      plist.NewDict(),
		platform: platform,
		gpuName:  gpuName,
		monitor:  monitor,
		macosVer: macosVersion,
		deviceID: sliceRunes(fullID, 5, len([]rune(fullID))),
	}

	branch, err := chooseIGPUBranch(ctx.deviceID, macosVersion)
	if err != nil {
		return nil, err
	}
	early := false
	switch branch {
	case 1:
		ctx.set("framebuffer-patch-enable", "01000000")
		ctx.set("framebuffer-singlelink", "01000000")
	case 2:
		err = ctx.snbBranch()
	case 3:
		err = ctx.ivyBranch()
	case 4:
		early, err = ctx.hdBranch()
	case 5:
		ctx.bBranch()
	case 6:
		early, err = ctx.skylakeBranch()
	case 7:
		early, err = ctx.kabyBranch()
	case 8:
		early, err = ctx.coffeeBranch()
	case 9:
		ctx.id8ABranch()
	}
	if err != nil {
		return nil, err
	}
	if early {
		return ctx.out, nil
	}

	fourK, err := ctx.resolutionAtLeastAny("3840x2160")
	if err != nil {
		return nil, err
	}
	if fourK {
		if pyrt.Equal(platform, "Laptop") {
			ctx.set("enable-max-pixel-clock-override", "01000000")
		}
		if _, ok := ctx.out.Get("framebuffer-stolenmem"); ok {
			ctx.out.Delete("framebuffer-stolenmem")
		}
		if _, ok := ctx.out.Get("framebuffer-fbmem"); ok {
			ctx.out.Delete("framebuffer-fbmem")
		}
	}

	for _, key := range ctx.out.Keys() {
		if key != "AAPL,ig-platform-id" && key != "device-id" {
			ctx.set("framebuffer-patch-enable", "01000000")
			break
		}
	}

	sorted := plist.NewDict()
	keys := ctx.out.Keys()
	sort.Strings(keys)
	for _, k := range keys {
		v, _ := ctx.out.Get(k)
		sorted.Set(k, v)
	}
	return sorted, nil
}

// chooseIGPUBranch returns the branch of the original's elif chain that matches
// the device ID (0 for none). Only the Skylake-era test evaluates the macOS
// version, and it does so only when the prefix matches, as Python does.
func chooseIGPUBranch(deviceID, macosVersion string) (int, error) {
	switch {
	case startsWithAny(deviceID, "0042", "0046"):
		return 1, nil
	case startsWithAny(deviceID, "01") && !lastCharIn(deviceID, "5", "6"):
		return 2, nil
	case startsWithAny(deviceID, "01"):
		return 3, nil
	case startsWithAny(deviceID, "04", "0A", "0C", "0D"):
		return 4, nil
	case startsWithAny(deviceID, "0B", "16"):
		return 5, nil
	case startsWithAny(deviceID, "09", "19"):
		below, err := darwinBelow(macosVersion, "22.0.0")
		if err != nil {
			return 0, err
		}
		if below {
			return 6, nil
		}
	}
	switch {
	case startsWithAny(deviceID, "09", "19", "59", "87C0"):
		return 7, nil
	case startsWithAny(deviceID, "3E", "87", "9B"):
		return 8, nil
	case startsWithAny(deviceID, "8A"):
		return 9, nil
	}
	return 0, nil
}

// lastCharIn mirrors s[-2] in options. Callers only reach it when s starts with
// "01", so s has at least two characters.
func lastCharIn(s string, options ...string) bool {
	r := []rune(s)
	if len(r) < 2 {
		return false
	}
	return inList(string(r[len(r)-2]), options...)
}

// snbBranch is the "01" branch with a leading digit other than 5 or 6.
func (c *igpuContext) snbBranch() error {
	native := []string{"0106", "1106", "1601", "0116", "0126", "0102"}
	if !inSlice(c.deviceID, native) {
		c.set("device-id", "26010000")
	}
	c.set("AAPL,snb-platform-id", "10000300")
	if pyrt.Equal(c.platform, "Desktop") {
		none, err := c.notConnected(true)
		if err != nil {
			return err
		}
		if none {
			c.set("AAPL,snb-platform-id", "00000500")
			c.set("device-id", "02010000")
		}
	} else if pyrt.Equal(c.platform, "Laptop") {
		fits, err := c.resolutionAtLeastAny("1600x900")
		if err != nil {
			return err
		}
		if fits {
			c.set("AAPL00,DualLink", "01000000")
		}
		c.set("AAPL,snb-platform-id", "00000100")
	}
	return nil
}

// ivyBranch is the "01" branch for the remaining prefixes.
func (c *igpuContext) ivyBranch() error {
	native := []string{"0152", "0156", "0162", "0166"}
	if !inSlice(c.deviceID, native) {
		c.set("device-id", "62010000")
	}
	switch {
	case pyrt.Equal(c.platform, "Desktop"):
		none, err := c.notConnected(true)
		if err != nil {
			return err
		}
		if none {
			c.set("AAPL,ig-platform-id", "07006201")
		}
		c.set("AAPL,ig-platform-id", "0A006601")
	case pyrt.Equal(c.platform, "NUC"):
		c.set("AAPL,ig-platform-id", "0B006601")
	case pyrt.Equal(c.platform, "Laptop"):
		c.set("AAPL,ig-platform-id", "03006601")
		fits, err := c.resolutionAtLeastAny("1600x900")
		if err != nil {
			return err
		}
		if fits {
			c.set("AAPL,ig-platform-id", "04006601")
			c.set("framebuffer-memorycount", "02000000")
			c.set("framebuffer-pipecount", "02000000")
			c.set("framebuffer-portcount", "04000000")
			c.set("framebuffer-stolenmem", "00000004")
			c.set("framebuffer-con1-enable", "01000000")
			c.set("framebuffer-con1-alldata", "020500000004000007040000030400000004000081000000040600000004000081000000")
		}
	}
	return nil
}

// hdBranch is the 04/0A/0C/0D family. It reports true when the original returns
// early (desktop without a connected monitor).
func (c *igpuContext) hdBranch() (bool, error) {
	native := []string{"0D26", "0A26", "0A2E", "0D22", "0412"}
	if !inSlice(c.deviceID, native) {
		c.set("device-id", "12040000")
	}
	switch {
	case pyrt.Equal(c.platform, "Desktop"):
		none, err := c.notConnected(true)
		if err != nil {
			return false, err
		}
		if none {
			c.set("AAPL,ig-platform-id", "04001204")
			return true, nil
		}
		c.set("AAPL,ig-platform-id", "0300220D")
		c.set("framebuffer-stolenmem", "00003001")
		c.set("framebuffer-fbmem", "00009000")
	case pyrt.Equal(c.platform, "NUC"):
		c.set("AAPL,ig-platform-id", "0300220D")
		c.set("framebuffer-cursormem", "00009000")
	case pyrt.Equal(c.platform, "Laptop"):
		c.set("AAPL,ig-platform-id", "0600260A")
		if startsWithAny(c.deviceID, "0A2", "0D2") {
			c.set("AAPL,ig-platform-id", "0500260A")
		}
		c.set("framebuffer-cursormem", "00009000")
	}
	return false, nil
}

// bBranch is the 0B/16 family.
func (c *igpuContext) bBranch() {
	native := []string{"0BD1", "0BD2", "0BD3", "1606", "160E", "161E", "1626", "1622", "1612", "162B"}
	if !inSlice(c.deviceID, native) {
		c.set("device-id", "26160000")
	}
	switch {
	case pyrt.Equal(c.platform, "Desktop"):
		c.set("AAPL,ig-platform-id", "07002216")
	case pyrt.Equal(c.platform, "NUC"):
		c.set("AAPL,ig-platform-id", "02001616")
	case pyrt.Equal(c.platform, "Laptop"):
		c.set("AAPL,ig-platform-id", "06002616")
	}
	c.set("framebuffer-stolenmem", "00003001")
	c.set("framebuffer-fbmem", "00009000")
}

// skylakeBranch is the 09/19 family before macOS 22. It does not exclude VGA.
func (c *igpuContext) skylakeBranch() (bool, error) {
	native := []string{"1916", "191E", "1926", "1927", "1912", "1932", "1902", "1917", "193B", "191B"}
	if !inSlice(c.deviceID, native) {
		c.set("device-id", "1B190000")
	}
	if inList(c.deviceID, "1906", "190B") {
		c.set("device-id", "02190000")
	}
	if startsWithAny(c.deviceID, "191E") {
		c.set("AAPL,ig-platform-id", "00001E19")
	}
	switch {
	case pyrt.Equal(c.platform, "Desktop"):
		none, err := c.notConnected(false)
		if err != nil {
			return false, err
		}
		if none {
			c.set("AAPL,ig-platform-id", "01001219")
			return true, nil
		}
		c.set("AAPL,ig-platform-id", "00001219")
	case pyrt.Equal(c.platform, "NUC"):
		c.set("AAPL,ig-platform-id", "02001619")
		if startsWithAny(c.deviceID, "1926", "1927") {
			c.set("AAPL,ig-platform-id", "02002619")
		} else if startsWithAny(c.deviceID, "1932", "193B", "193A", "193D") {
			c.set("AAPL,ig-platform-id", "05003B19")
		}
	case pyrt.Equal(c.platform, "Laptop"):
		c.set("AAPL,ig-platform-id", "00001619")
		if startsWithAny(c.deviceID, "1902", "1906", "190B") {
			c.set("AAPL,ig-platform-id", "00001B19")
		}
	}
	c.set("framebuffer-stolenmem", "00003001")
	c.set("framebuffer-fbmem", "00009000")
	return false, nil
}

// kabyBranch is the 09/19/59/87C0 family. It does not exclude VGA.
func (c *igpuContext) kabyBranch() (bool, error) {
	native := []string{"5912", "5916", "591B", "591C", "591E", "5926", "5927", "5923", "87C0"}
	if !inSlice(c.deviceID, native) {
		c.set("device-id", "16590000")
	}
	if startsWithAny(c.deviceID, "5917", "5916", "5921") {
		c.set("AAPL,ig-platform-id", "00001659")
	}
	switch {
	case pyrt.Equal(c.platform, "Desktop"):
		none, err := c.notConnected(false)
		if err != nil {
			return false, err
		}
		if none {
			c.set("AAPL,ig-platform-id", "03001259")
			return true, nil
		}
		c.set("AAPL,ig-platform-id", "00001259")
	case pyrt.Equal(c.platform, "NUC"):
		c.set("AAPL,ig-platform-id", "00001E59")
		if startsWithAny(c.deviceID, "5912", "591B", "591A", "591D") {
			c.set("AAPL,ig-platform-id", "00001B59")
		} else if startsWithAny(c.deviceID, "5926", "5927") {
			c.set("AAPL,ig-platform-id", "02002659")
		}
	case pyrt.Equal(c.platform, "Laptop"):
		c.set("AAPL,ig-platform-id", "00001B59")
		if startsWithAny(c.deviceID, "5917", "87C0") {
			c.set("AAPL,ig-platform-id", "00001659")
		} else {
			c.set("framebuffer-con1-alldata", "01050A00000800008701000002040A000008000087010000")
			c.set("framebuffer-con1-enable", "01000000")
			c.set("#framebuffer-con1-alldata", "01050A00000800008701000003060A000004000087010000")
			c.set("#framebuffer-con1-enable", "01000000")
		}
	}
	c.set("framebuffer-stolenmem", "00003001")
	c.set("framebuffer-fbmem", "00009000")
	return false, nil
}

// coffeeBranch is the 3E/87/9B family. It does not exclude VGA.
func (c *igpuContext) coffeeBranch() (bool, error) {
	native := []string{"3E9B", "3EA5", "3EA6", "3E92", "3E91", "3E98", "9BC8", "9BC5", "9BC4"}
	if !inSlice(c.deviceID, native) {
		c.set("device-id", "9B3E0000")
	}
	switch {
	case pyrt.Equal(c.platform, "Desktop"):
		none, err := c.notConnected(false)
		if err != nil {
			return false, err
		}
		if none {
			if startsWithAny(c.deviceID, "9B") {
				c.set("AAPL,ig-platform-id", "0300C89B")
			} else {
				c.set("AAPL,ig-platform-id", "0300913E")
			}
			return true, nil
		}
		below, err := darwinBelow(c.macosVer, "19.5.0")
		if err != nil {
			return false, err
		}
		if below {
			c.set("AAPL,ig-platform-id", "07009B3E")
		} else {
			c.set("AAPL,ig-platform-id", "00009B3E")
		}
		c.set("framebuffer-stolenmem", "00003001")
	case pyrt.Equal(c.platform, "NUC"):
		c.set("AAPL,ig-platform-id", "07009B3E")
		if startsWithAny(c.deviceID, "3EA5", "3EA8") {
			c.set("AAPL,ig-platform-id", "0000A53E")
		}
	case pyrt.Equal(c.platform, "Laptop"):
		c.set("AAPL,ig-platform-id", "0900A53E")
		if startsWithAny(c.deviceID, "3EA9", "3EA0") {
			c.set("AAPL,ig-platform-id", "00009B3E")
		}
		c.set("framebuffer-stolenmem", "00003001")
		c.set("framebuffer-fbmem", "00009000")
	}
	return false, nil
}

// id8ABranch is the 8A family.
func (c *igpuContext) id8ABranch() {
	native := []string{"FF05", "8A70", "8A71", "8A51", "8A5C", "8A5D", "8A52", "8A53", "8A5A", "8A5B"}
	if !inSlice(c.deviceID, native) {
		c.set("device-id", "528A0000")
	}
	c.set("AAPL,ig-platform-id", "0200518A")
	c.set("enable-dbuf-early-optimizer", "01000000")
	c.set("enable-dvmt-calc-fix", "01000000")
	c.set("enable-cdclk-frequency-fix", "01000000")
	c.set("framebuffer-stolenmem", "00003001")
	c.set("framebuffer-fbmem", "00009000")
}
