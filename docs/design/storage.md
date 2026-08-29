---
layout: default
title: "Storage"
description: "Block storage handling in urunc"
---

# Block storage handling in 'urunc'

## Overview

When a unikernel is configured to boot from a block device (e.g. a
`devmapper` snapshot), 'urunc' has to hand over the underlying storage of
a host bind mount to the guest as a raw block device, instead of the bind
mount itself. This page documents the loop-device handling that this
requires, in particular the `autoclear` flag of Linux loop devices.

## Why 'urunc' clears the loop device autoclear flag

Bind mounts that are backed by a filesystem image (e.g. an `ext2` image)
are mounted by the kernel through a loop device. By default, such loop
devices are created with the `autoclear` flag set, which means the kernel
destroys the loop device as soon as it is no longer mounted anywhere (see
the kernel documentation on the
[`autoclear` sysfs attribute](https://www.kernel.org/doc/Documentation/ABI/testing/sysfs-block-loop)).

In `getBlockVolumes` (`pkg/unikontainers/block.go`), 'urunc' unmounts the
host bind mount so that the underlying block device is free to be passed
to the guest instead. If the loop device still has `autoclear` set at that
point, the kernel would tear it down the moment the unmount happens,
leaving 'urunc' with no block device to attach to the sandbox.

To avoid this, before unmounting, 'urunc' clears the `autoclear` flag of
the loop device with `setLoopAutoclear(mInfo.Source, false)`. This keeps
the loop device alive after the unmount, so it can be handed to the guest
as a block device.

## Restoring autoclear on delete

Once the container is deleted, `restoreBlockVolumes`
(`pkg/unikontainers/block.go`) re-mounts the host bind mount at its
original mount point and, if 'urunc' had cleared the `autoclear` flag for
that loop device during create, restores it with
`setLoopAutoclear(b.Source, true)`. This returns the loop device to its
original, kernel-managed lifecycle once 'urunc' no longer needs it to
outlive the mount.

## Caveat: delete is not guaranteed to run

The restore step above only happens as part of the delete path. If the
container's `delete` is never invoked (e.g. the higher-level runtime or
node crashes and cleanup never runs), the loop device is left with
`autoclear` cleared and the original host bind mount left unmounted.
Operators relying on automatic loop device cleanup (e.g. `losetup -D`, or
the kernel reclaiming the device once unused) should be aware that a
loop device touched by 'urunc' may not be cleaned up automatically unless
`urunc delete` actually runs for that container.
