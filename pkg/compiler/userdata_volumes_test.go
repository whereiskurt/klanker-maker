package compiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

// km-volumes: hibernate-safe additional volumes (spec
// docs/superpowers/specs/2026-09-20-hibernate-volume-validation-design.md).
// These tests pin the userdata half of the design: the fstab lines are gone,
// one validated-mount unit and one bounded sleep shim take their place, the
// policy renders from the profile, and a profile with no additional volume
// is untouched.

func volumesProfile() *profile.SandboxProfile {
	p := baseProfile()
	p.Spec.Runtime.AdditionalVolume = &profile.AdditionalVolumeSpec{Size: 30, MountPoint: "/data"}
	p.Spec.Runtime.AdditionalSnapshots = []profile.AdditionalSnapshotSpec{{SnapshotID: "snap-0d7b1093da2702612", MountPoint: "/repos"}}
	return p
}

// extractHeredoc returns the body of `cat > <path> << '<tag>' … <tag>` in out.
func extractHeredoc(t *testing.T, out, path, tag string) string {
	t.Helper()
	start := strings.Index(out, "cat > "+path+" << '"+tag+"'\n")
	if start < 0 {
		t.Fatalf("no heredoc for %s (tag %s) in rendered userdata", path, tag)
	}
	body := out[start+len("cat > "+path+" << '"+tag+"'\n"):]
	end := strings.Index(body, "\n"+tag+"\n")
	if end < 0 {
		t.Fatalf("unterminated heredoc %s", tag)
	}
	return body[:end+1]
}

// The sleep shim is the one thing here that can wedge hibernation (addendum
// §C): it must bound km-volumes and exit 0 whatever happens. Execute the
// rendered shim under sh with a km-volumes that hangs.
func TestUserdataVolumes_SleepShimIsBoundedAndExitsZero(t *testing.T) {
	out, err := generateUserData(volumesProfile(), "sb-vol", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	shim := extractHeredoc(t, out, "/usr/lib/systemd/system-sleep/km-volumes", "KMVOLSLEEP")
	dir := t.TempDir()
	fake := filepath.Join(dir, "km-volumes")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nsleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(dir, "km-volumes.log")
	shim = strings.ReplaceAll(shim, "/opt/km/bin/km-volumes", fake)
	shim = strings.ReplaceAll(shim, "/var/log/km-volumes.log", log)
	shim = strings.ReplaceAll(shim, "timeout 15 ", "timeout 1 ")
	shim = strings.ReplaceAll(shim, "timeout 45 ", "timeout 1 ")
	for _, phase := range []string{"pre", "post"} {
		start := time.Now()
		cmd := exec.Command("sh", "-c", shim, "sh", phase, "hibernate")
		cmd.Env = append(os.Environ(), "PATH="+dir) // no systemctl on the test host: the post branch must still exit 0
		if err := cmd.Run(); err != nil {
			t.Fatalf("%s: shim must exit 0 even when km-volumes hangs or systemctl is absent: %v", phase, err)
		}
		if d := time.Since(start); d > 5*time.Second {
			t.Fatalf("%s: shim did not bound km-volumes (took %s)", phase, d)
		}
	}
	// The post branch must NOT run the binary inline: the re-probe has to wait
	// for the hypervisor to finish restoring the EBS functions, which takes
	// longer than any budget a sleep hook may hold. It hands off to a unit.
	if !strings.Contains(shim, "systemctl start --no-block km-volumes-resume.service") {
		t.Errorf("post:hibernate must hand off to km-volumes-resume.service; shim:\n%s", shim)
	}
	if strings.Contains(shim, "km-volumes post-sleep") {
		t.Errorf("post-sleep must not run inline in the hook; shim:\n%s", shim)
	}
	// A non-hibernate transition must be a no-op that still exits 0.
	if err := exec.Command("sh", "-c", shim, "sh", "pre", "suspend").Run(); err != nil {
		t.Fatalf("suspend must be ignored with exit 0: %v", err)
	}
}

func TestUserdataVolumes_NoFstabLineAndUnitPresent(t *testing.T) {
	out, err := generateUserData(volumesProfile(), "sb-vol", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, ">> /etc/fstab") {
		t.Error("additional volumes must no longer be written to /etc/fstab (mount-by-UUID is how the cross-write happened)")
	}
	for _, want := range []string{
		"sidecars/km-volumes",
		"/etc/systemd/system/km-volumes.service",
		"Type=oneshot",
		"Environment=KM_VOLUMES_ON_MISMATCH=refuse",
		"ExecStart=/opt/km/bin/km-volumes mount --fallback /data:f --fallback /repos:g",
		"systemctl enable km-volumes.service",
		"/opt/km/bin/km-volumes mount --fallback /data:f --fallback /repos:g",
		"/etc/systemd/system/km-volumes-resume.service",
		"TimeoutStartSec=240",
		"ExecStart=/opt/km/bin/km-volumes post-sleep",
		`/opt/km/bin/km-volumes manifest --mountpoint "/data" --bdm "f"`,
		`/opt/km/bin/km-volumes manifest --mountpoint "/repos" --bdm "g"`,
		`--from-snapshot "snap-0d7b1093da2702612"`,
		"${FSTYPE}", // SNAP-07: fs type must still be the bash variable, never a hardcoded ext4
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered userdata missing %q", want)
		}
	}
	// First-boot validation must call the binary, never `systemctl start`: a
	// start job from inside cloud-final deadlocks the bootstrap. And the unit
	// must not order After=cloud-final for the same reason (proven live).
	if strings.Contains(out, "systemctl start km-volumes.service") {
		t.Error("first boot must invoke km-volumes mount directly, not systemctl start (deadlocks inside cloud-final)")
	}
	unit := extractHeredoc(t, out, "/etc/systemd/system/km-volumes.service", "KMVOLUNIT")
	for _, line := range strings.Split(unit, "\n") {
		if strings.HasPrefix(line, "After=") && strings.Contains(line, "cloud-final") {
			t.Errorf("km-volumes.service must not be After=cloud-final.service: %s", line)
		}
	}
	// The additional volume (not snapshot-derived) must NOT carry --from-snapshot.
	dataLine := out[strings.Index(out, `manifest --mountpoint "/data"`):]
	dataLine = dataLine[:strings.Index(dataLine, "\n")]
	if strings.Contains(dataLine, "--from-snapshot") {
		t.Errorf("/data manifest call must not claim a snapshot: %s", dataLine)
	}
}

func TestUserdataVolumes_NoVolumesRendersNothing(t *testing.T) {
	out, err := generateUserData(baseProfile(), "sb-novol", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, absent := range []string{"km-volumes", "system-sleep"} {
		if strings.Contains(out, absent) {
			t.Errorf("profile without additional volumes must not render %q", absent)
		}
	}
}

func TestUserdataVolumes_PolicyRendersFromProfile(t *testing.T) {
	p := volumesProfile()
	p.Spec.Runtime.OnVolumeMismatch = "reboot"
	out, err := generateUserData(p, "sb-vol", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Environment=KM_VOLUMES_ON_MISMATCH=reboot") {
		t.Error("onVolumeMismatch: reboot not rendered into the unit")
	}
}
