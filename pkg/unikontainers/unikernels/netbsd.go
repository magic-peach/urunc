// Copyright (c) 2023-2026, Nubificus LTD
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package unikernels

import (
	"fmt"
	"strings"

	"github.com/urunc-dev/urunc/pkg/unikontainers/types"
)

const NetBSDUnikernel string = "netbsd"

type NetBSD struct {
	Command string
	Monitor string
	Net     NetBSDNet
	Block   []NetBSDBlock
}

type NetBSDNet struct {
	Address string
	Gateway string
	Mask    string
}

type NetBSDBlock struct {
	ID       string
	HostPath string
}

func (n *NetBSD) CommandString() (string, error) {
	return "console=com root=ld0a", nil
}

func (n *NetBSD) SupportsBlock() bool {
	return true
}

func (n *NetBSD) SupportsFS(fsType string) bool {
	switch fsType {
	case "ext2":
		return true
	default:
		return false
	}
}

func (n *NetBSD) MonitorNetCli(ifName string, mac string) string {
	switch n.Monitor {
	case "qemu":
		netOption := " -netdev tap,id=net0,script=no,downscript=no,ifname=" + ifName
		netOption += " -device virtio-net-device,netdev=net0,mac=" + mac
		return netOption
	default:
		return ""
	}
}

func (n *NetBSD) MonitorBlockCli() []types.MonitorBlockArgs {
	if len(n.Block) == 0 {
		return nil
	}
	switch n.Monitor {
	case "qemu":
		bcli1 := fmt.Sprintf(" -device virtio-blk-device,serial=%s,drive=%s", n.Block[0].ID, n.Block[0].ID)
		bcli2 := fmt.Sprintf(" -drive format=raw,if=none,id=%s,file=%s", n.Block[0].ID, n.Block[0].HostPath)
		return []types.MonitorBlockArgs{
			{
				ExactArgs: bcli1 + bcli2,
			},
		}
	case "firecracker":
		id := n.Block[0].ID
		return []types.MonitorBlockArgs{
			{
				ID: id,
				Path: n.Block[0].HostPath,
			},
		}
	default:
		return nil
	}
}

func (n *NetBSD) MonitorCli() types.MonitorCliArgs {
	switch n.Monitor {
	case "qemu":
		monArgs := " -M microvm,rtc=on,acpi=off,pic=off,accel=kvm -global virtio-mmio.force-legacy=false -no-reboot -display none -nodefaults -serial stdio"
		return types.MonitorCliArgs{
			OtherArgs: monArgs,
		}
	default:
		return types.MonitorCliArgs{}
	}
}

func (n *NetBSD) Init(data types.UnikernelParams) error {
	// if Mask is empty, there is no network support
	if data.Net.Mask != "" {
		n.Net.Address = data.Net.IP
		n.Net.Gateway = data.Net.Gateway
		n.Net.Mask = data.Net.Mask
	}
	n.Block = make([]NetBSDBlock, 0, len(data.Block))
	for _, blk := range data.Block {
		newBlk := NetBSDBlock{
			ID:       blk.ID,
			HostPath: blk.Source,
		}
		n.Block = append(n.Block, newBlk)
	}

	n.Command = strings.Join(data.CmdLine, " ")
	n.Monitor = data.Monitor

	return nil
}

func newNetBSD() *NetBSD {
	netbsdStruct := new(NetBSD)
	return netbsdStruct
}
