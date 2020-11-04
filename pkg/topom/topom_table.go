package topom

import (
	"github.com/CodisLabs/codis/pkg/models"
	"github.com/CodisLabs/codis/pkg/utils/errors"
	"github.com/CodisLabs/codis/pkg/utils/log"
	"github.com/CodisLabs/codis/pkg/utils/math2"
	"github.com/CodisLabs/codis/pkg/utils/rpc"
	"github.com/CodisLabs/codis/pkg/utils/sync2"
	"math/rand"
	"sort"
	"strconv"
	"time"
)

func (s *Topom) checkClusterStatus() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx, err := s.newContext()
	if err != nil {
		return err
	}
	for _, g := range ctx.group {
		for _, x := range g.Servers {
			if v := s.stats.servers[x.Addr]; v != nil {
				if v.Timeout == true {
					return errors.Errorf("pika addr-[%s] is timeout, please check it !", x.Addr)
				}
				if v.Error != nil {
					return errors.Errorf("pika addr-[%s] is ERROR, please check it !", x.Addr)
				}
			}
		}
	}
	return nil
}

func checkPikaResult(results map[string]*PikaResult) error {
	for i, r := range results {
		switch {
		case r.Timeout == true:
			return errors.Errorf("pika addr-[%s] is timeout, please check it !", i)
		case r.Error != nil:
			return errors.Errorf("pika addr-[%s] is ERROR: %s", i, r.Error)
		default:
		}
	}
	return nil
}

func (s *Topom) CreateTable(name string, num, id int) error {
	if err := s.pikaInGroupExist(); err != nil {
		return err
	}
	if err := s.checkClusterStatus(); err != nil {
		return err
	}
	tid, err := s.createMeta(name, num, id)
	if err != nil {
		return err
	}
	if _, err := s.createTableForPika(tid, num); err != nil {
		return err
	}
	if _, err := s.PikaAddSlot(tid); err != nil {
		return err
	}
	if _, err := s.PikaSlotsSlaveof(tid); err != nil {
		return err
	}
	return nil
}

func (s *Topom) pikaInGroupExist() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx, err := s.newContext()
	if err != nil {
		return err
	}
	for _, g := range ctx.group {
		if len(g.Servers) == 0 {
			return errors.Errorf("group-[%d] has no pika", g.Id)
		}
	}
	return nil
}

func (s *Topom) CreateSlotForMeta(tid int) error {
	distribution, err := s.CaculateDistribution(tid)
	if err != nil {
		return err
	}
	slots := []*models.SlotMapping{}
	for _, d := range distribution {
		for i := d.Begin; i <= d.End; i++ {
			slots = append(slots, &models.SlotMapping{
				Id:      i,
				GroupId: d.GroupId,
				TableId: tid,
			})
		}
	}
	if err := s.SlotsAssignGroup(tid, slots); err != nil {
		return err
	}
	return nil
}

func (s *Topom) DeleteSlotForMeta(tid int) error {
	num, err := s.getSlotNum(tid)
	if err != nil {
		return err
	}
	slots := make([]*models.SlotMapping, num)
	for i := range slots {
		slots[i] = &models.SlotMapping{
			Id:      i,
			GroupId: 0,
			TableId: tid,
		}
	}
	if err := s.SlotsAssignOffline(tid, slots); err != nil {
		return err
	}
	return nil
}

func (s *Topom) CreateTableForMeta(name string, num, id int) error {
	tid, err := s.createMeta(name, num, id)
	if err != nil {
		return err
	}
	if err := s.CreateSlotForMeta(tid); err != nil {
		return err
	}
	return nil
}

func (s *Topom) CreateTableForPika(tid int) (map[string]*PikaResult, error) {
	if err := s.checkClusterStatus(); err != nil {
		return nil, err
	}
	num, err := s.getSlotNum(tid)
	if err != nil {
		return nil, err
	}
	if results, err := s.createTableForPika(tid, num); err != nil {
		return nil, err
	} else {
		return results, nil
	}
}

func (s *Topom) getSlotNum(tid int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx, err := s.newContext()
	if err != nil {
		return 0, err
	}
	if _, ok := ctx.table[tid]; ok != true {
		return 0, errors.Errorf("table-[%d] dose not exist", tid)
	}
	return ctx.table[tid].MaxSlotMum, nil
}

const letterBytes = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
const stringLength = 8
const delimiter = "+"

func InitSeed() {
	rand.Seed(time.Now().UnixNano())
}

func RandStringBytes(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = letterBytes[rand.Intn(len(letterBytes))]
	}
	return string(b)
}

func (s *Topom) generateAuth(prefix string) string {
	InitSeed()
	return prefix + delimiter + RandStringBytes(stringLength)
}

func (s *Topom) createMeta(name string, num, id int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx, err := s.newContext()
	if err != nil {
		return 0, err
	}
	if err := models.ValidateTable(name); err != nil {
		return 0, err
	}
	var tid int
	if id != -1 {
		if _, ok := ctx.table[id]; ok == true {
			return 0, errors.Errorf("tid-[%d] already exists", id)
		}
		if id >= ctx.tableMeta.Id {
			return 0, errors.Errorf("tid-[%d] is large than self-increase Id-[%d],please change tid and retry", id, ctx.tableMeta.Id)
		}
		tid = id
	} else {
		tid = ctx.tableMeta.Id
		// don't create table 0
		if tid == 0 {
			tid = 1
		}
	}
	for _, t := range ctx.table {
		if name == t.Name {
			return 0, errors.Errorf("name-[%s] already exists", name)
		}
		if tid == t.Id {
			return 0, errors.Errorf("tid-[%d] already exists", tid)
		}
	}
	if id == -1 {
		defer s.dirtyTableMetaCache()
		tm := &models.TableMeta{Id: tid + 1}
		if err := s.storeCreateTableMeta(tm); err != nil {
			return 0, err
		}
	}
	defer s.dirtyTableCache(tid)
	t := &models.Table{
		Id:         tid,
		Name:       name,
		MaxSlotMum: num,
		Auth:       s.generateAuth(name),
		IsBlocked:  false,
	}
	if err := s.storeCreateTable(t); err != nil {
		return 0, err
	}
	if err := s.syncCreateTable(ctx, t); err != nil {
		log.Warnf("table-[%s] tid-[%d] sync to proxy failed", t.Name, t.Id)
		return 0, err
	}
	return tid, nil
}

type SlotDistribution struct {
	GroupId int  `json:"GroupId"`
	Begin   int  `json:"Begin"`
	End     int  `json:"End"`
	Done    bool `json:"Done"`
}

func (s *Topom) allocateSlot(slotNum int) ([]*SlotDistribution, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx, err := s.newContext()
	if err != nil {
		return nil, err
	}
	group := models.SortGroup(ctx.group)
	groupSize := len(group)
	if groupSize == 0 {
		return nil, errors.Errorf("allocate slot fail because of no group")
	}

	distribution := make([]*SlotDistribution, math2.MinInt(groupSize, slotNum))
	distance := slotNum / groupSize
	remainder := slotNum % groupSize
	if distance == 0 {
		for i := 0; i < slotNum; i++ {
			distribution[i] = &SlotDistribution{
				GroupId: group[i].Id,
				Begin:   i,
				End:     i,
				Done:    false}
		}
	} else {
		for i := 0; i < remainder; i++ {
			distribution[i] = &SlotDistribution{
				GroupId: group[i].Id,
				Begin:   (distance + 1) * i,
				End:     (distance+1)*(i+1) - 1,
				Done:    false}
		}
		for i := remainder; i < groupSize; i++ {
			distribution[i] = &SlotDistribution{
				GroupId: group[i].Id,
				Begin:   distance*i + remainder,
				End:     distance*(i+1) - 1 + remainder,
				Done:    false}
		}
	}
	return distribution, nil
}

func (s *Topom) CaculateDistribution(tid int) ([]*SlotDistribution, error) {
	num, err := s.getSlotNum(tid)
	if err != nil {
		return nil, err
	}
	if distribution, err := s.allocateSlot(num); err != nil {
		return nil, err
	} else {
		return distribution, nil
	}
}

func (s *Topom) GetDistributionFromPika(tid int) (map[string]string, error) {
	if err := s.checkClusterStatus(); err != nil {
		return nil, err
	}

	w, err := s.RefreshPikaInfo(time.Second * 30)
	if err != nil {
		log.Warnf("check pika info slot error: %s", err)
	}
	if w != nil {
		w.Wait()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for addr, p := range s.manager.servers {
		if p.Timeout == true {
			return nil, errors.Errorf("pika-[addr] get cluster info slot timeout", addr)
		} else if p.Error != nil {
			return nil, p.Error
		}
	}
	slotSet := make(map[string]string)
	for addr, p := range s.manager.servers {
		var slice []int
		if p.Table[tid] == nil {
			continue
		}
		for id := range p.Table[tid].Slot {
			slice = append(slice, id)
		}
		sort.Ints(slice)
		var slot string
		for _, e := range slice {
			slot += strconv.Itoa(e) + ","
		}
		slotSet[addr] = slot
		log.Infof("pika-[%s] table-[%d] slot: %s", addr, tid, slot)
	}
	return slotSet, nil
}

func (s *Topom) createTableForPika(tid, slotNum int) (map[string]*PikaResult, error) {
	log.Warnf("Create table-[%d] slot num-[%d] foreach pika", tid, slotNum)
	command := s.stats.redisp.CreateTable(tid, slotNum)
	if results, err := s.pikaExecuteForeach(time.Second*30, command); err != nil {
		return nil, err
	} else if err = checkPikaResult(results); err != nil {
		return results, err
	}
	return nil, nil
}

func (s *Topom) PikaAddSlot(tid int) (map[string]*PikaResult, error) {
	distribution, err := s.CaculateDistribution(tid)
	if err != nil {
		return nil, err
	}
	for _, d := range distribution {
		log.Warnf("table-[%d] add slot beg-[%d] end[%d]for group-[%d]", tid, d.Begin, d.End, d.GroupId)
		command := s.stats.redisp.AddSlots(tid, d.Begin, d.End)
		if results, err := s.pikaExecuteForGroup(time.Second*30, command, d.GroupId); err != nil {
			return nil, err
		} else if err = checkPikaResult(results); err != nil {
			return results, err
		}
	}
	if err := s.CreateSlotForMeta(tid); err != nil {
		return nil, err
	}
	return nil, nil
}

func (s *Topom) PikaDelSlot(tid int) (map[string]*PikaResult, error) {
	d, err := s.GetDistributionFromPika(tid)
	if err != nil {
		return nil, err
	}
	command := s.stats.redisp.DelSlots(tid, d)
	if results, err := s.pikaExecuteForeach(time.Second*30, command); err != nil {
		return nil, err
	} else if err = checkPikaResult(results); err != nil {
		return results, err
	}
	return nil, nil
}

func (s *Topom) PikaSlotsSlaveof(tid int) (map[string]*PikaResult, error) {
	log.Warnf("get into pikaSlotsSlaveof")
	if err := s.checkClusterStatus(); err != nil {
		return nil, err
	}
	group, err := s.getGroup()
	if err != nil {
		return nil, err
	}
	for _, g := range group {
		if master, err := s.getMasterFromGroup(g.Id); err != nil {
			return nil, err
		} else if master != "" {
			log.Warnf("table-[%d] slots slaveof in group-[%d]", tid, g.Id)
			command := s.stats.redisp.SlotSlaveofAll(master, tid)
			if results, err := s.pikaExecuteForSlave(time.Second*30, command, g.Id); err != nil {
				return nil, err
			} else if err = checkPikaResult(results); err != nil {
				return results, err
			}
		}
	}
	return nil, nil
}

func (s *Topom) PikaSlotsSlaveofNoOne(tid int) (map[string]*PikaResult, error) {
	if err := s.checkClusterStatus(); err != nil {
		return nil, err
	}
	command := s.stats.redisp.SlotSlaveofAll("no:one", tid)
	if results, err := s.pikaExecuteForSlave(time.Second*30, command, 0); err != nil {
		return nil, err
	} else if err = checkPikaResult(results); err != nil {
		return results, err
	}
	return nil, nil
}

func (s *Topom) getGroup() ([]*models.Group, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx, err := s.newContext()
	if err != nil {
		return nil, err
	}
	return models.SortGroup(ctx.group), nil
}

func (s *Topom) getMasterFromGroup(gid int) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx, err := s.newContext()
	if err != nil {
		return "", err
	}
	g := ctx.group[gid]
	if g != nil && g.Servers[0] != nil {
		return g.Servers[0].Addr, nil
	} else {
		return "", nil
	}
}

type PikaResult struct {
	Done     bool
	Error    *rpc.RemoteError `json:"error,omitempty"`
	UnixTime int64            `json:"unixtime"`
	Timeout  bool             `json:"timeout,omitempty"`
}

func (s *Topom) newPikaExecuter(addr string, timeout time.Duration, command func(addr string) error) *PikaResult {
	var ch = make(chan struct{})
	result := &PikaResult{Done: false}

	go func() {
		defer close(ch)

		err := command(addr)
		if err != nil {
			result.Error = rpc.NewRemoteError(err)
		} else {
			result.Done = true
		}
	}()

	select {
	case <-ch:
		return result
	case <-time.After(timeout):
		return &PikaResult{Timeout: true}
	}
}

func (s *Topom) pikaExecuteForeach(timeout time.Duration, command func(addr string) error) (map[string]*PikaResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx, err := s.newContext()
	if err != nil {
		return nil, err
	}
	var fut sync2.Future
	for _, g := range ctx.group {
		for _, x := range g.Servers {
			fut.Add()
			go func(addr string) {
				result := s.newPikaExecuter(addr, timeout, command)
				result.UnixTime = time.Now().Unix()
				fut.Done(addr, result)
			}(x.Addr)
		}
	}
	results := make(map[string]*PikaResult)
	for k, v := range fut.Wait() {
		results[k] = v.(*PikaResult)
	}
	return results, nil
}

func (s *Topom) pikaExecuteForGroup(timeout time.Duration, command func(addr string) error, gid int) (map[string]*PikaResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx, err := s.newContext()
	if err != nil {
		return nil, err
	}
	var fut sync2.Future
	if g := ctx.group[gid]; g == nil {
		return nil, errors.Errorf("group-[%s] dose not exist", gid)
	} else {
		for _, x := range g.Servers {
			fut.Add()
			go func(addr string) {
				result := s.newPikaExecuter(addr, timeout, command)
				result.UnixTime = time.Now().Unix()
				fut.Done(addr, result)
			}(x.Addr)
		}
	}
	results := make(map[string]*PikaResult)
	for k, v := range fut.Wait() {
		results[k] = v.(*PikaResult)
	}
	return results, nil
}

func (s *Topom) pikaExecuteForSlave(timeout time.Duration, command func(addr string) error, gid int) (map[string]*PikaResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx, err := s.newContext()
	if err != nil {
		return nil, err
	}
	var fut sync2.Future
	if gid == 0 {
		for _, g := range ctx.group {
			if len(g.Servers) > 1 {
				for _, x := range g.Servers[1:] {
					fut.Add()
					go func(addr string) {
						result := s.newPikaExecuter(addr, timeout, command)
						result.UnixTime = time.Now().Unix()
						fut.Done(addr, result)
					}(x.Addr)
				}
			}
		}
	} else {
		if g := ctx.group[gid]; g == nil {
			return nil, errors.Errorf("group-[%s] dose not exist", gid)
		} else {
			if len(g.Servers) > 1 {
				for _, x := range g.Servers[1:] {
					fut.Add()
					go func(addr string) {
						result := s.newPikaExecuter(addr, timeout, command)
						result.UnixTime = time.Now().Unix()
						fut.Done(addr, result)
					}(x.Addr)
				}
			}
		}
	}
	results := make(map[string]*PikaResult)
	for k, v := range fut.Wait() {
		results[k] = v.(*PikaResult)
	}
	return results, nil
}

func (s *Topom) ListTable() ([]*models.Table, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx, err := s.newContext()
	if err != nil {
		return nil, err
	}
	tSlice := make([]*models.Table, len(ctx.table))
	for _, t := range ctx.table {
		tSlice = append(tSlice, t)
	}
	return tSlice, nil
}

func (s *Topom) GetTable(tid int) (*models.Table, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx, err := s.newContext()
	if err != nil {
		return nil, err
	}
	if t, ok := ctx.table[tid]; ok {
		return t, nil
	} else {
		return nil, errors.Errorf("invalid table id = %d, not exist", tid)
	}
}

func (s *Topom) RemoveTable(tid int) error {
	if err := s.checkClusterStatus(); err != nil {
		return err
	}
	if _, err := s.PikaSlotsSlaveofNoOne(tid); err != nil {
		return err
	}
	time.Sleep(time.Second * 5)
	if _, err := s.PikaDelSlot(tid); err != nil {
		return err
	}
	if _, err := s.deleteTableForPika(tid); err != nil {
		return err
	}
	if err := s.RemoveTableFromMeta(tid); err != nil {
		return err
	}
	return nil
}

func (s *Topom) RemoveTableFromMeta(tid int) error {
	if err := s.DeleteSlotForMeta(tid); err != nil {
		return err
	}

	if err := s.removeTableFromMeta(tid); err != nil {
		return err
	}
	return nil
}

func (s *Topom) removeTableFromMeta(tid int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx, err := s.newContext()
	if err != nil {
		return err
	}
	t := ctx.table[tid]
	if t == nil {
		return errors.Errorf("table-[%d] not exist", tid)
	}
	for i, s := range ctx.slots[tid] {
		if s.GroupId != 0 {
			return errors.Errorf("table-[%d] slot-[%d] is still in use, please off-line slot first", tid, i)
		}
	}
	for _, m := range ctx.slots[tid] {
		if err := s.storeRemoveSlotMapping(tid, m); err != nil {
			return err
		}
	}
	if err := s.syncRemoveTable(ctx, t); err != nil {
		log.Warnf("table-[%s] tid-[%d] sync to proxy failed", t.Name, t.Id)
		return err
	}
	defer s.dirtyTableCache(tid)
	return s.storeRemoveTable(t)
}

func (s *Topom) RemoveTableFromPika(tid int) (map[string]*PikaResult, error) {
	if err := s.checkClusterStatus(); err != nil {
		return nil, err
	}
	if results, err := s.deleteTableForPika(tid); err != nil {
		return nil, err
	} else {
		if err = checkPikaResult(results); err != nil {
			return results, err
		}
	}
	return nil, nil
}

func (s *Topom) deleteTableForPika(tid int) (map[string]*PikaResult, error) {
	log.Warnf("Delete table-[%d] foreach pika", tid)
	command := s.stats.redisp.DeleteTable(tid)
	if results, err := s.pikaExecuteForeach(time.Second*30, command); err != nil {
		return nil, err
	} else {
		return results, nil
	}
}

func (s *Topom) RenameTable(tid int, name, auth string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx, err := s.newContext()
	if err != nil {
		return err
	}
	if err := models.ValidateTable(name); err != nil {
		return err
	}
	t, ok := ctx.table[tid]
	if ok == false {
		return errors.Errorf("table-[%d] dose not exist", tid)
	}
	for i := range ctx.table {
		if i != tid {
			if name == ctx.table[i].Name {
				return errors.Errorf("name-[%s] already exists", name)
			}
			if auth == ctx.table[i].Auth {
				return errors.Errorf("auth-[%s] already exists", name)
			}
		}
	}
	defer s.dirtyTableCache(tid)
	t.Name = name
	t.Auth = auth
	if err := s.storeUpdateTable(t); err != nil {
		return err
	}
	if err := s.syncFillTable(ctx, t); err != nil {
		log.Warnf("table-[%s] tid-[%d] sync to proxy failed", t.Name, t.Id)
		return err
	}
	return nil
}

func (s *Topom) GetTableMeta() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx, err := s.newContext()
	if err != nil {
		return 0, err
	}
	return ctx.tableMeta.Id, nil
}

func (s *Topom) SetTableMeta(id int) error {
	defer s.dirtyTableMetaCache()
	tm := &models.TableMeta{Id: id}
	return s.storeCreateTableMeta(tm)
}

func (s *Topom) SetTableBlock(tid int, isBlock bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx, err := s.newContext()
	if err != nil {
		return err
	}
	t, ok := ctx.table[tid]
	if ok == false {
		return errors.Errorf("table-[%d] dose not exist", tid)
	}
	defer s.dirtyTableCache(tid)
	t.IsBlocked = isBlock
	if err := s.storeUpdateTable(t); err != nil {
		return err
	}
	if err := s.syncFillTable(ctx, t); err != nil {
		log.Warnf("table-[%s] tid-[%d] sync to proxy failed", t.Name, t.Id)
		return err
	}
	return nil
}
