// Command km-volumes owns a sandbox's additional EBS volumes end to end so that
// a hibernate/resume can never silently cross-write them.
//
// On the first cold boot the userdata block calls `manifest` once per volume,
// recording each volume's physical identity — the serial from a LIVE NVMe
// Identify Controller (the EBS volume id), its size, and its filesystem UUID —
// in /var/lib/km/volumes.json. On every boot a oneshot unit runs `mount`, which
// resolves devices by live serial (never by BDM letter, never by UUID, never by
// the kernel's cached sysfs serial), validates size and filesystem identity
// against the manifest, and mounts only on a full match; anything else refuses
// that entry, writes <mountpoint>/.km-mount-refused, records the reason in
// /var/lib/km/volumes.state and emits a volume_mount_refused audit event.
//
// Around hibernation a system-sleep shim runs `pre-sleep` (a bounded unmount
// ladder) and `post-sleep` (an unconditional PCI remove+rescan of every non-root
// NVMe controller — the same from-scratch probe a reboot gives — then `mount`
// again, one retry, then the refuse|reboot policy). Both always exit 0.
// `repair` is operator-only: e2fsck against ext4 backup superblocks for a
// refused volume. Every kernel-facing call sits behind the System interface so
// the decision logic is unit-tested with a fake.
//
// Design: docs/superpowers/specs/2026-09-20-hibernate-volume-validation-design.md
package main
