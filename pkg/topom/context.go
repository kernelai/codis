// Copyright 2016 CodisLabs. All Rights Reserved.
// Licensed under the MIT (MIT-LICENSE.txt) license.

package topom

import (
	"net"
	"sync"
	"time"

	"github.com/CodisLabs/codis/pkg/models"
	"github.com/CodisLabs/codis/pkg/utils"
	"github.com/CodisLabs/codis/pkg/utils/errors"
	"github.com/CodisLabs/codis/pkg/utils/log"
	"github.com/CodisLabs/codis/pkg/utils/math2"
)

const MaxSlotNum = models.MaxSlotNum

type context struct {
	slots map[int][]*models.SlotMapping
	group map[int]*models.Group
	proxy map[string]*models.Proxy
	table map[int]*models.Table

	sentinel *models.Sentinel

	hosts struct {
		sync.Mutex
		m map[string]net.IP
	}
	method int
}

func (ctx *context) getSlotMapping(tid int, sid int) (*models.SlotMapping, error) {
	if len(ctx.slots[tid]) != ctx.table[tid].MaxSlotMum {
		return nil, errors.Errorf("invalid table-[%d] number of slots = %d/%d",tid, len(ctx.slots[tid]), ctx.table[tid].MaxSlotMum)
	}
	if sid >= 0 && sid < ctx.table[tid].MaxSlotMum {
		return ctx.slots[tid][sid], nil
	}
	return nil, errors.Errorf("table-[%d] slot-[%d] doesn't exist", tid, sid)
}

func (ctx *context) getSlotMappingsByGroupId(gid int) []*models.SlotMapping {
	var slots = []*models.SlotMapping{}
	for _, s := range ctx.slots {
		for _, m := range s {
			if m.GroupId == gid || m.Action.TargetId == gid {
				slots = append(slots, m)
			}
		}

	}
	return slots
}

func (ctx *context) maxSlotActionIndex(tid int) (maxIndex int) {
	for _, m := range ctx.slots[tid] {
		if m.Action.State != models.ActionNothing {
			maxIndex = math2.MaxInt(maxIndex, m.Action.Index)
		}
	}
	return maxIndex
}

func (ctx *context) isSlotLocked(m *models.SlotMapping) bool {
	switch m.Action.State {
	case models.ActionNothing, models.ActionPending:
		return ctx.isGroupLocked(m.GroupId)
	case models.ActionPreparing:
		return ctx.isGroupLocked(m.GroupId)
	case models.ActionPrepared:
		return true
	case models.ActionMigrating:
		return ctx.isGroupLocked(m.GroupId) || ctx.isGroupLocked(m.Action.TargetId)
	case models.ActionFinished:
		return ctx.isGroupLocked(m.Action.TargetId)
	default:
		log.Panicf("slot-[%d] action state is invalid:\n%s", m.Id, m.Encode())
	}
	return false
}

func (ctx *context) toSlot(m *models.SlotMapping, p *models.Proxy) *models.Slot {
	slot := &models.Slot{
		Id:     m.Id,
		TableId: m.TableId,
		Locked: ctx.isSlotLocked(m),

		ForwardMethod: ctx.method,
	}
	switch m.Action.State {
	case models.ActionNothing, models.ActionPending:
		slot.BackendAddr = ctx.getGroupMaster(m.GroupId)
		slot.BackendAddrGroupId = m.GroupId
		slot.ReplicaGroups = ctx.toReplicaGroups(m.GroupId, p)
	case models.ActionPreparing:
		slot.BackendAddr = ctx.getGroupMaster(m.GroupId)
		slot.BackendAddrGroupId = m.GroupId
	case models.ActionPrepared:
		fallthrough
	case models.ActionMigrating:
		slot.BackendAddr = ctx.getGroupMaster(m.Action.TargetId)
		slot.BackendAddrGroupId = m.Action.TargetId
		slot.MigrateFrom = ctx.getGroupMaster(m.GroupId)
		slot.MigrateFromGroupId = m.GroupId
	case models.ActionFinished:
		slot.BackendAddr = ctx.getGroupMaster(m.Action.TargetId)
		slot.BackendAddrGroupId = m.Action.TargetId
	default:
		log.Panicf("slot-[%d] action state is invalid:\n%s", m.Id, m.Encode())
	}
	return slot
}

func (ctx *context) lookupIPAddr(addr string) net.IP {
	ctx.hosts.Lock()
	defer ctx.hosts.Unlock()
	ip, ok := ctx.hosts.m[addr]
	if !ok {
		if tcpAddr := utils.ResolveTCPAddrTimeout(addr, 50*time.Millisecond); tcpAddr != nil {
			ctx.hosts.m[addr] = tcpAddr.IP
			return tcpAddr.IP
		} else {
			ctx.hosts.m[addr] = nil
			return nil
		}
	}
	return ip
}

func (ctx *context) toReplicaGroups(gid int, p *models.Proxy) [][]string {
	g := ctx.group[gid]
	switch {
	case g == nil:
		return nil
	case g.Promoting.State != models.ActionNothing:
		return nil
	case len(g.Servers) <= 1:
		return nil
	}
	var dc string
	var ip net.IP
	if p != nil {
		dc = p.DataCenter
		ip = ctx.lookupIPAddr(p.AdminAddr)
	}
	getPriority := func(s *models.GroupServer) int {
		if ip == nil || dc != s.DataCenter {
			return 2
		}
		if ip.Equal(ctx.lookupIPAddr(s.Addr)) {
			return 0
		} else {
			return 1
		}
	}
	var groups [3][]string
	for _, s := range g.Servers {
		if s.ReplicaGroup {
			p := getPriority(s)
			groups[p] = append(groups[p], s.Addr)
		}
	}
	var replicas [][]string
	for _, l := range groups {
		if len(l) != 0 {
			replicas = append(replicas, l)
		}
	}
	return replicas
}

func (ctx *context) toSlotSlice(slots []*models.SlotMapping, p *models.Proxy) []*models.Slot {
	var slice = make([]*models.Slot, len(slots))
	for i, m := range slots {
		slice[i] = ctx.toSlot(m, p)
	}
	return slice
}

func (ctx *context) toAllSlotSlice(slots map[int][]*models.SlotMapping, p *models.Proxy) []*models.Slot {
	var slice []*models.Slot
	for _, m := range slots {
		slice = append(slice, ctx.toSlotSlice(m, p)...)
	}
	return slice
}

func (ctx *context) toTableSlice(tables map[int]*models.Table) []*models.Table {
	var slice []*models.Table
	for _, t := range tables {
		slice = append(slice, t)
	}
	for _, s := range slice {
		log.Infof( "table slot", s.Id)
	}
	return slice
}

func (ctx *context) getTable(tid int) (*models.Table, error) {
	if t := ctx.table[tid]; t != nil {
		return t, nil
	}
	return nil, errors.Errorf("table-[%d] doesn't exist", tid)
}

func (ctx *context) getGroup(gid int) (*models.Group, error) {
	if g := ctx.group[gid]; g != nil {
		return g, nil
	}
	return nil, errors.Errorf("group-[%d] doesn't exist", gid)
}

func (ctx *context) getGroupIndex(g *models.Group, addr string) (int, error) {
	for i, x := range g.Servers {
		if x.Addr == addr {
			return i, nil
		}
	}
	return -1, errors.Errorf("group-[%d] doesn't have server-[%s]", g.Id, addr)
}

func (ctx *context) getGroupByServer(addr string) (*models.Group, int, error) {
	for _, g := range ctx.group {
		for i, x := range g.Servers {
			if x.Addr == addr {
				return g, i, nil
			}
		}
	}
	return nil, -1, errors.Errorf("server-[%s] doesn't exist", addr)
}

func (ctx *context) maxSyncActionIndex() (maxIndex int) {
	for _, g := range ctx.group {
		for _, x := range g.Servers {
			if x.Action.State == models.ActionPending {
				maxIndex = math2.MaxInt(maxIndex, x.Action.Index)
			}
		}
	}
	return maxIndex
}

func (ctx *context) minSyncActionIndex() string {
	var d *models.GroupServer
	for _, g := range ctx.group {
		for _, x := range g.Servers {
			if x.Action.State == models.ActionPending {
				if d == nil || x.Action.Index < d.Action.Index {
					d = x
				}
			}
		}
	}
	if d == nil {
		return ""
	}
	return d.Addr
}

func (ctx *context) getGroupMaster(gid int) string {
	if g := ctx.group[gid]; g != nil && len(g.Servers) != 0 {
		return g.Servers[0].Addr
	}
	return ""
}

func (ctx *context) getGroupMasters() map[int]string {
	var masters = make(map[int]string)
	for _, g := range ctx.group {
		if len(g.Servers) != 0 {
			masters[g.Id] = g.Servers[0].Addr
		}
	}
	return masters
}

func (ctx *context) getGroupIds() map[int]bool {
	var groups = make(map[int]bool)
	for _, g := range ctx.group {
		groups[g.Id] = true
	}
	return groups
}

func (ctx *context) isGroupInUse(gid int) bool {
	for _, s := range ctx.slots {
		for _, m := range s {
			if m.GroupId == gid || m.Action.TargetId == gid {
				return true
			}
		}
	}
	return false
}

func (ctx *context) isGroupLocked(gid int) bool {
	if g := ctx.group[gid]; g != nil {
		switch g.Promoting.State {
		case models.ActionNothing:
			return false
		case models.ActionPreparing:
			return false
		case models.ActionPrepared:
			return true
		case models.ActionFinished:
			return false
		default:
			log.Panicf("invalid state of group-[%d] = %s", g.Id, g.Encode())
		}
	}
	return false
}

func (ctx *context) isGroupPromoting(gid int) bool {
	if g := ctx.group[gid]; g != nil {
		return g.Promoting.State != models.ActionNothing
	}
	return false
}

func (ctx *context) getProxy(token string) (*models.Proxy, error) {
	if p := ctx.proxy[token]; p != nil {
		return p, nil
	}
	return nil, errors.Errorf("proxy-[%s] doesn't exist", token)
}

func (ctx *context) maxProxyId() (maxId int) {
	for _, p := range ctx.proxy {
		maxId = math2.MaxInt(maxId, p.Id)
	}
	return maxId
}
