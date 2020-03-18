// Copyright 2016 CodisLabs. All Rights Reserved.
// Licensed under the MIT (MIT-LICENSE.txt) license.

package etcdclientv3

import (
	"fmt"
	"github.com/CodisLabs/codis/pkg/utils/assert"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/context"

	"go.etcd.io/etcd/clientv3"

	"github.com/CodisLabs/codis/pkg/utils/errors"
	"github.com/CodisLabs/codis/pkg/utils/log"
)

var ErrClosedClient = errors.New("use of closed etcd client")
var ErrKeyNotExists = errors.New("key not exists")
var ErrKeyAlreadyExists = errors.New("key already exists")

type Client struct {
	sync.Mutex
	kapi clientv3.KV
	c    *clientv3.Client

	closed  bool
	timeout time.Duration

	cancel  context.CancelFunc
	context context.Context

	leaseID clientv3.LeaseID
}

func New(addrlist string, auth string, timeout time.Duration) (*Client, error) {
	endpoints := strings.Split(addrlist, ",")
	for i, s := range endpoints {
		if s != "" && !strings.HasPrefix(s, "http://") {
			endpoints[i] = "http://" + s
		}
	}
	if timeout <= 0 {
		timeout = time.Second * 5
	}

	config := clientv3.Config{
		Endpoints: endpoints,
		DialTimeout: time.Second * 5,
	}

	if auth != "" {
		split := strings.SplitN(auth, ":", 2)
		if len(split) != 2 || split[0] == "" {
			return nil, errors.Errorf("invalid auth")
		}
		config.Username = split[0]
		config.Password = split[1]
	}

	c, err := clientv3.New(config)
	if err != nil {
		return nil, errors.Trace(err)
	}

	client := &Client{
		kapi: clientv3.NewKV(c), timeout: timeout, c: c,
	}
	client.context, client.cancel = context.WithCancel(context.Background())
	return client, nil
}

func (c *Client) Close() error {
	c.Lock()
	defer c.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	c.cancel()
	return c.c.Close()
}

func (c *Client) isClosed() bool {
	c.Lock()
	defer c.Unlock()
	return c.closed
}

func (c *Client) newContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(c.context, c.timeout)
}

func (c *Client) Create(path string, data []byte) error {
	c.Lock()
	defer c.Unlock()
	return c.createLocked(path, data)
}

func (c *Client) createLocked(path string, data []byte) error {
	if c.closed {
		return errors.Trace(ErrClosedClient)
	}
	if strings.IndexRune(path, '/') != 0 {
		return fmt.Errorf("path must starts with '/'")
	}

	lastSlashIdx := strings.LastIndexByte(path, '/')
	if lastSlashIdx != 0 {
		err := c.createLocked(path[:lastSlashIdx], []byte{})
		if err != nil && err != ErrKeyAlreadyExists  {
			return err
		}
	}
	if lastSlashIdx == len(path) -1 {
		return nil
	}

	ctx, cancel := c.newContext()
	defer cancel()
	log.Debugf("etcd create node %s", path)

	req := clientv3.OpPut(path, string(data))
	cond := clientv3.Compare(clientv3.Version(path), "=", 0)
	resp, err := c.kapi.Txn(ctx).If(cond).Then(req).Commit()
	if err != nil {
		log.Debugf("etcd create node %s failed: %s", path, err)
		return errors.Trace(err)
	}
	if !resp.Succeeded {
		err = ErrKeyAlreadyExists
		log.Debugf("etcd create node %s failed: %s", path, err)
		return err
	}
	log.Debugf("etcd create OK")
	return nil
}

func (c *Client) Update(path string, data []byte) error {
	if strings.LastIndexByte(path, '/') == len(path) - 1 {
		path = path[:len(path)-1]
	}
	c.Lock()
	defer c.Unlock()
	if c.closed {
		return errors.Trace(ErrClosedClient)
	}
	cntx, cancel := c.newContext()
	defer cancel()
	log.Debugf("etcd update node %s", path)
	_, err := c.kapi.Put(cntx, path, string(data))
	if err != nil {
		log.Debugf("etcd update node %s failed: %s", path, err)
		return errors.Trace(err)
	}
	log.Debugf("etcd update OK")
	return nil
}

func (c *Client) Delete(path string) error {
	if strings.LastIndexByte(path, '/') == len(path) - 1 {
		path = path[:len(path)-1]
	}
	c.Lock()
	defer c.Unlock()
	if c.closed {
		return errors.Trace(ErrClosedClient)
	}
	cntx, cancel := c.newContext()
	defer cancel()
	log.Debugf("etcd delete node %s", path)
	_, err := c.kapi.Delete(cntx, path, clientv3.WithPrefix())
	if err != nil {
		log.Debugf("etcd delete node %s failed: %s", path, err)
		return errors.Trace(err)
	}
	log.Debugf("etcd delete OK")
	return nil
}

func (c *Client) Read(path string, must bool) ([]byte, error) {
	if strings.LastIndexByte(path, '/') == len(path) - 1 {
		path = path[:len(path)-1]
	}
	c.Lock()
	defer c.Unlock()
	if c.closed {
		return nil, errors.Trace(ErrClosedClient)
	}
	cntx, cancel := c.newContext()
	defer cancel()
	// By default, etcd clientv3 use same config as old 'client.GetOptions{Quorum: true}'
	r, err := c.kapi.Get(cntx, path)
	if err != nil {
		log.Debugf("etcd read node %s failed: %s", path, err)
		return nil, errors.Trace(err)
	}
	// Otherwise should return error instead.
	if r.Count == 0 {
		if must {
			return nil, ErrKeyNotExists
		}
		return nil, nil
	}
	return r.Kvs[0].Value, nil
}

func (c *Client) List(path string, must bool) ([]string, error) {
	c.Lock()
	defer c.Unlock()
	return c.listLocked(path, must)
}

func (c *Client) listLocked(path string, must bool) ([]string, error) {
	if c.closed {
		return nil, errors.Trace(ErrClosedClient)
	}
	if strings.LastIndexByte(path, '/') == len(path) - 1 {
		path = path[:len(path)-1]
	}
	cntx, cancel := c.newContext()
	defer cancel()
	opts := []clientv3.OpOption{
		clientv3.WithPrefix(),
		clientv3.WithSort(clientv3.SortByKey, clientv3.SortAscend),
	}
	r, err := c.kapi.Get(cntx, path, opts...)
	switch {
	case err != nil:
		log.Debugf("etcd list node %s failed: %s", path, err)
		return nil, errors.Trace(err)
	default:
		var paths []string
		for _, kv := range r.Kvs {
			paths = append(paths, string(kv.Key))
		}
		var listedPaths []string
		tid := -1
		for i:= 0; i < len(paths); i++ {
			p := paths[i]
			if strings.LastIndexByte(p, '/') == len(path) {
				listedPaths = append(listedPaths, p)
			}
			if p == path {
				tid = i
			}
		}
		if tid == -1 && must {
			// For backward compatibility, expect error if path not exists
			err = ErrKeyNotExists
			log.Debugf("etcd node %s not exists: %s", path, err)
			return nil, err
		}
		return listedPaths, nil
	}
}

func (c *Client) CreateEphemeral(path string, data []byte) (<-chan struct{}, error) {
	if strings.LastIndexByte(path, '/') == len(path) - 1 {
		path = path[:len(path)-1]
	}
	c.Lock()
	defer c.Unlock()
	if c.closed {
		return nil, errors.Trace(ErrClosedClient)
	}
	cntxLease, cancelLease := c.newContext()
	defer cancelLease()
	log.Debugf("etcd create-ephemeral node %s", path)

	timeoutInSeconds := int64(c.timeout) / int64(time.Second)
	if timeoutInSeconds < 1 {
		timeoutInSeconds = 1
	}
	lease, err := c.c.Grant(cntxLease, timeoutInSeconds)
	if err != nil {
		log.Debugf("etcd create-lease for node %s failed: %s", path, err)
		return nil, errors.Trace(err)
	}

	defer func() {
		if err != nil {
			rCtx, rCancel := c.newContext()
			_, _ = c.c.Revoke(rCtx, lease.ID)
			rCancel()
		}
	}()

	cntx, cancel := c.newContext()
	defer cancel()

	req := clientv3.OpPut(path, string(data), clientv3.WithLease(lease.ID))
	cond := clientv3.Compare(clientv3.Version(path), "=", 0)
	var resp *clientv3.TxnResponse
	resp, err = c.kapi.Txn(cntx).If(cond).Then(req).Commit()
	if err != nil {
		log.Debugf("etcd create-ephemeral node %s failed: %s", path, err)
		return nil, errors.Trace(err)
	}
	if !resp.Succeeded {
		err = ErrKeyAlreadyExists
		log.Debugf("etcd create-ephemeral node %s failed: %s", path, err)
		return nil, err
	}
	log.Debugf("etcd create-ephemeral OK")
	return runRefreshEphemeral(c, path, lease.ID), nil
}

func runRefreshEphemeral(c *Client, path string, leaseID clientv3.LeaseID) <-chan struct{} {
	signal := make(chan struct{})
	go func() {
		defer close(signal)
		for {
			if err := c.refreshEphemeral(path, leaseID); err != nil {
				return
			} else {
				time.Sleep(c.timeout / 2)
			}
		}
	}()
	return signal
}

func (c *Client) refreshEphemeral(path string, leaseID clientv3.LeaseID) error {
	c.Lock()
	defer c.Unlock()
	if c.closed {
		return errors.Trace(ErrClosedClient)
	}
	cntx, cancel := c.newContext()
	defer cancel()
	log.Debugf("etcd refresh-ephemeral node %s", path)
	_, err := c.c.KeepAliveOnce(cntx, leaseID)
	if err != nil {
		log.Debugf("etcd refresh-ephemeral node %s failed: %s", path, err)
		return errors.Trace(err)
	}
	log.Debugf("etcd refresh-ephemeral OK")
	return nil
}

func (c *Client) MkDir(path string) error {
	return c.Create(path, []byte{})
}

// Simulate v2 createInOrder with transactions.
// Unfortunately etcd v3 doesn't have api to compare keys,
// we have to add a 10 bytes prefix to the stored data.
// You have to use ReadEphemeralInOrder() to retrieve the data.
func (c *Client) CreateEphemeralInOrder(path string, data []byte) (<-chan struct{}, string, error) {
	if strings.LastIndexByte(path, '/') == len(path) - 1 {
		path = path[:len(path)-1]
	}
	c.Lock()
	defer c.Unlock()
	if c.closed {
		return nil, "", errors.Trace(ErrClosedClient)
	}
	var err error
	cntxLease, cancelLease := c.newContext()
	defer cancelLease()
	log.Debugf("etcd create-ephemeral node %s", path)

	var lease *clientv3.LeaseGrantResponse
	lease, err = c.c.Grant(cntxLease, int64(c.timeout) / int64(time.Second))
	if err != nil {
		log.Debugf("etcd create-ephemeral get lease for node %s failed: %s", path, err)
		return nil, "", errors.Trace(err)
	}
	defer func() {
		if err != nil {
			rCtx, rCancel := c.newContext()
			_, _ = c.c.Revoke(rCtx, lease.ID)
			rCancel()
		}
	} ()

	var key string
	cntx, cancel := c.newContext()
	defer cancel()

	for {
		var maxKey string
		maxKey, err = c.getMaxKeyLocked(path)
		if err != nil {
			log.Debugf("etcd create-ephemeral get prev-version for node %s failed: %s", path, err)
			return nil, "", err
		}
		var v int64 = -1
		if len(maxKey) == len(path) + 10 {
			versionStr := maxKey[len(path) + 1:]
			v, err = strconv.ParseInt(versionStr, 10, 64)
			if err != nil {
				log.Debugf("etcd create-ephemeral get previous version for node %s failed: %s", path, err)
				return nil, "", err
			}
		} else if len(maxKey) != len(path) {
			return nil, "", fmt.Errorf("corrupted dir '%s', contains non-formal key '%s'", path, maxKey)
		}
		v++
		if v >= 1000000000 {
			return nil, "", fmt.Errorf("version overflowed max 1000000000")
		}

		key = fmt.Sprintf("%s/%09d", path, v)
		// Add prefix to value so that we can compare the version of key with clientv3.Value(path),
		// unfortunately etcd v3 doesn't have api to compare keys.
		// Uses have to retrieve the data by themselves.
		val := fmt.Sprintf("%09d_%s", v, string(data))

		var resp *clientv3.TxnResponse
		req := clientv3.OpPut(key, val, clientv3.WithLease(lease.ID))
		cond := clientv3.Compare(clientv3.Value(path).WithPrefix(), "<", fmt.Sprintf("%09d", v))
		resp, err = c.kapi.Txn(cntx).If(cond).Then(req).Commit()
		if err != nil {
			log.Debugf("etcd create-ephemeral node %s failed: %s", path, err)
			return nil, "", errors.Trace(err)
		}
		if !resp.Succeeded {
			_, err = c.c.KeepAliveOnce(cntx, lease.ID)
			if err != nil {
				log.Debugf("etcd refresh-ephemeral node %s failed: %s", path, err)
				return nil, "", errors.Trace(err)
			}
			continue
		}
		break
	}
	log.Debugf("etcd create-ephemeral OK")
	c.leaseID = lease.ID
	return runRefreshEphemeral(c, key, lease.ID), key, nil
}

func (c *Client) GetMinKey(path string) (string, error) {
	c.Lock()
	defer c.Unlock()
	return c.getMinKeyLocked(path)
}

func (c *Client) getMinKeyLocked(path string) (string, error) {
	return c.getMinMaxKeyLocked(path, clientv3.SortAscend)
}

func (c *Client) GetMaxKey(path string) (string, error) {
	c.Lock()
	defer c.Unlock()
	return c.getMaxKeyLocked(path)
}

func (c *Client) getMaxKeyLocked(path string) (string, error) {
	return c.getMinMaxKeyLocked(path, clientv3.SortDescend)
}

func (c *Client) getMinMaxKeyLocked(path string, sortOrder clientv3.SortOrder) (string, error) {
	if c.closed {
		return "", errors.Trace(ErrClosedClient)
	}
	if strings.LastIndexByte(path, '/') == len(path) - 1 {
		path = path[:len(path)-1]
	}
	cntx, cancel := c.newContext()
	defer cancel()
	opts := []clientv3.OpOption{
		clientv3.WithPrefix(),
		clientv3.WithSort(clientv3.SortByKey, sortOrder),
		clientv3.WithLimit(1),
	}
	r, err := c.kapi.Get(cntx, path, opts...)
	switch {
	case err != nil:
		log.Debugf("etcd list node %s failed: %s", path, err)
		return "", errors.Trace(err)
	default:
		var paths []string
		for _, kv := range r.Kvs {
			paths = append(paths, string(kv.Key))
		}
		if len(paths) == 0 {
			return "", ErrKeyNotExists
		}
		assert.Must(len(paths) == 1)
		return paths[0], nil
	}
}

func (c *Client) ReadEphemeralInOrder(path string, must bool) ([]byte, error) {
	data, err := c.Read(path, must)
	if err != nil {
		return data, err
	}
	if len(data) == 0 {
		return data, nil
	}
	if len(data) < 10 {
		return nil, fmt.Errorf("len(data) < 10, corrupted value")
	}
	return data[10:], nil
}

func (c *Client) WatchInOrder(path string) (<-chan struct{}, []string, error) {
	if strings.LastIndexByte(path, '/') == len(path) - 1 {
		path = path[:len(path)-1]
	}
	err := c.MkDir(path)
	if err != nil && err != ErrKeyAlreadyExists {
		return nil, nil, err
	}

	c.Lock()
	defer c.Unlock()
	if c.closed {
		return nil, nil, errors.Trace(ErrClosedClient)
	}

	log.Debugf("etcd watch-inorder node %s", path)

	paths, err := c.listLocked(path, true)
	if err != nil {
		return nil, nil, err
	}

	signal := make(chan struct{})
	watched := make(chan struct{})
	go func() {
		defer close(signal)

		cancellableCtx, canceller := context.WithCancel(clientv3.WithRequireLeader(c.context))
		// GC watched chan, otherwise will memory leak.
		defer canceller()
		ch := c.c.Watch(cancellableCtx, path, clientv3.WithPrefix())
		close(watched)
		for resp := range ch {
			for _, event := range resp.Events {
				log.Debugf("etcd watch-inorder node %s update, event: %v", path, event)
				return
			}
		}
	}()

	select {
	case <-time.After(c.timeout):
		return nil, nil, fmt.Errorf("watch timeouted")
	case <-watched:
		break
	}

	log.Debugf("etcd watch-inorder OK")
	return signal, paths, nil
}
