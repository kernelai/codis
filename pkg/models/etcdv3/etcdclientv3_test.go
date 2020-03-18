package etcdclientv3

import (
    "bytes"
    "context"
    "fmt"
    "github.com/CodisLabs/codis/pkg/utils/assert"
    "github.com/CodisLabs/codis/pkg/utils/errors"
    "github.com/CodisLabs/codis/pkg/utils/log"
    "os"
    "sync"
    "sync/atomic"
    "testing"
    "time"
)

const testRoot = "/test/"


func getClient(t testing.TB) *Client {
    return getClientWithTimeout(t, time.Second * 15)
}

func getClientNonClearData(t testing.TB) *Client {
    return getClientNonClearDataWithTimeout(t, time.Second * 15)
}

func getClientWithTimeout(t testing.TB, timeout time.Duration) *Client {
    c := getClientNonClearDataWithTimeout(t, timeout)
    if c == nil {
        return nil
    }
    err := c.Delete(testRoot)
    if err != nil {
        t.Errorf("failed to cleanup etcd path '%s', detail: '%s'", testRoot, err.Error())
        return nil
    }
    return c
}

func getClientNonClearDataWithTimeout(t testing.TB, timeout time.Duration) *Client {
    etcdAddr := os.Getenv("ETCD_TEST_SERVER_ADDR")
    if len(etcdAddr) == 0 {
        t.Errorf("etcd server addr not found, you must set env ETCD_TEST_SERVER_ADDR first")
        return nil
    }
    c, err := New(etcdAddr, "", timeout)
    if err != nil {
        t.Errorf("can't connection to %s, detail: '%s'", etcdAddr, err.Error())
        return nil
    }
    return c
}

func TestClient_CreateReadDelete(t *testing.T) {
    c := getClient(t)
    assert.Must(c != nil)

    key := testRoot + "key"
    val := "value"
    err := c.Create(key, []byte(val))
    if err != nil {
        t.Errorf("create failed, detail: '%s'", err.Error())
        return
    }

    valRead, err := c.Read(key, true)
    if err != nil {
        t.Errorf("can't read key '%s', detail: '%s'", key, err.Error())
        return
    }
    if string(valRead) != val {
        t.Errorf("read value not correct, exp '%s', but met '%s'", val, valRead)
        return
    }

    err = c.Create(key, []byte("xxx"))
    if err != ErrKeyAlreadyExists {
        t.Errorf("expect error 'ErrKeyAlreadyExists' while creating key '%s', but met '%v'", key, err)
        return
    }

    valRead1, err := c.Read(key, true)
    if err != nil {
        t.Errorf("can't read key '%s', detail: '%s'", key, err.Error())
        return
    }
    if string(valRead1) != val {
        t.Errorf("read value not correct, exp '%s', but met '%s'", val, valRead)
        return
    }

    err = c.Delete(key)
    if err != nil {
        t.Errorf("can't delete key '%s', detail: '%s'", key, err.Error())
        return
    }

    notExistKey := key + "xxxxxx"
    err = c.Delete(notExistKey)
    if err != nil {
        t.Errorf("can't delete key '%s', detail: '%s'", key, err.Error())
        return
    }

    _, err = c.Read(key, true)
    if err != ErrKeyNotExists {
        t.Errorf("expect error '%s', but met '%v'", ErrKeyNotExists.Error(), err)
        return
    }
    _, err = c.Read(key, false)
    if err != nil {
        t.Errorf("expect no error if must is false")
        return
    }
}

func TestClient_List(t *testing.T) {
    c := getClient(t)
    assert.Must(c != nil)

    keyspace1 := testRoot + "keyspace1"
    keyspace1Value := "this is keyspace1"
    err := c.Create(keyspace1, []byte(keyspace1Value))
    if err != nil {
        t.Errorf("create failed, detail: '%s'", err.Error())
        return
    }

    valRead, err := c.Read(keyspace1, true)
    if err != nil {
        t.Errorf("can't read key '%s', detail: '%s'", keyspace1, err.Error())
        return
    }
    if string(valRead) != keyspace1Value {
        t.Errorf("read value not correct, exp '%s', but met '%s'", keyspace1Value, valRead)
        return
    }

    keyspace2 := testRoot + "keyspace2"
    keyspace2Value := "this is keyspace2"
    err = c.Create(keyspace2, []byte(keyspace2Value))
    if err != nil {
        t.Errorf("can't read key '%s', detail: '%s'", keyspace2, err.Error())
        return
    }

    valRead, err = c.Read(keyspace2, true)
    if err != nil {
        t.Errorf("can't read key '%s', detail: '%s'", keyspace2, err.Error())
        return
    }
    if string(valRead) != keyspace2Value {
        t.Errorf("read value not correct, exp '%s', but met '%s'", keyspace1Value, valRead)
        return
    }

    _, err = c.Read(testRoot+"key", true)
    if err != ErrKeyNotExists {
        t.Errorf("expected error key not exists")
        return
    }

    key2 := keyspace2 + "/" + "key2"
    err = c.Update(key2, []byte("v1"))
    if err != nil {
        t.Errorf("failed to update key '%s' to v1, detail: '%s'", key2, err.Error())
        return
    }

    finalVal := "v100"
    err = c.Update(key2, []byte(finalVal))
    if err != nil {
        t.Errorf("failed to update key '%s' to '%s', detail: '%s'", key2, finalVal, err.Error())
        return
    }

    valRead, err = c.Read(key2, true)
    if err != nil {
        t.Errorf("failed to read key '%s', detail: '%s'", key2, err.Error())
        return
    }
    if string(valRead) != finalVal {
        t.Errorf("failed to read key '%s', exp: '%s', but met: '%s'", key2, finalVal, string(valRead))
        return
    }

    keys, err := c.List(testRoot, true)
    if err != nil {
        t.Errorf("can't list key '%s', detail: '%s'", testRoot, err.Error())
        return
    }
    if len(keys) != 2 {
        t.Errorf("list value length not correct, exp 2, but met %d", len(keys))
        return
    }
    if !(keys[0] == keyspace1 && keys[1] == keyspace2) {
        t.Errorf("list values not correct, exp %v, but met %v", []string{keyspace1, keyspace2}, keys)
        return
    }

    keys, err = c.List(testRoot+"xxx", true)
    if err != ErrKeyNotExists {
        t.Errorf("can't list key '%s', detail: '%v'", testRoot+"xxx", err)
        return
    }
    keys, err = c.List(testRoot+"xxx", false)
    if err != nil {
        t.Errorf("expect no error if must is false, but met '%v'", err)
        return
    }
    if len(keys) != 0 {
        t.Errorf("list error, length not match, expect 0 but met %d", len(keys))
        return
    }

    keys, err = c.List(keyspace2, true)
    if err != nil {
        t.Errorf("can't list key '%s', detail: '%s'", keyspace2, err.Error())
        return
    }
    if len(keys) != 1 {
        t.Errorf("list error, length not match, expect 1 but met %d", len(keys))
        return
    }
    if keys[0] != key2 {
        t.Errorf("list error, key not match, expect '%s' but met %s", key2, keys[0])
        return
    }

    keys, err = c.List(keyspace1, true)
    if err != nil {
        t.Errorf("can't list key '%s', detail: '%s'", keyspace1, err.Error())
        return
    }
    if len(keys) != 0 {
        t.Errorf("list error, length not match, expect 0 but met %d", len(keys))
        return
    }
}

func TestClient_List2(t *testing.T) {
    c := getClient(t)
    assert.Must(c != nil)

    err := c.Create(testRoot + "a/b/c1", []byte{})
    if err != nil {
        t.Errorf(err.Error())
        return
    }
    err = c.Create(testRoot + "a/b/c2/", []byte{})
    if err != nil {
        t.Errorf(err.Error())
        return
    }
    err = c.Create(testRoot + "a/bbb", []byte{})
    if err != nil {
        t.Errorf(err.Error())
        return
    }

    paths, err := c.List(testRoot, true)
    if err != nil {
        t.Errorf(err.Error())
        return
    }
    if len(paths) != 1 || paths[0] != testRoot + "a" {
        t.Errorf("paths not match, expect %v, but met %v", []string{testRoot + "a"}, paths)
        return
    }

    paths, err = c.List(testRoot + "a", true)
    if err != nil {
        t.Errorf(err.Error())
        return
    }
    if len(paths) != 2 || paths[0] != testRoot + "a/b" || paths[1] != testRoot + "a/bbb" {
        t.Errorf("paths not match, expect %v, but met %v",
            []string{testRoot + "a/b", testRoot + "a/bbb"}, paths)
        return
    }

    paths, err = c.List(testRoot + "a/b/", true)
    if err != nil {
        t.Errorf(err.Error())
        return
    }
    if len(paths) != 2 || paths[0] != testRoot + "a/b/c1" || paths[1] != testRoot + "a/b/c2" {
        t.Errorf("paths not match, expect %v, but met %v",
            []string{testRoot + "a/b/c1", testRoot + "a/b/c2"}, paths)
        return
    }

    paths, err = c.List(testRoot + "a/b", true)
    if err != nil {
        t.Errorf(err.Error())
        return
    }
    if len(paths) != 2 || paths[0] != testRoot + "a/b/c1" || paths[1] != testRoot + "a/b/c2" {
        t.Errorf("paths not match, expect %v, but met %v",
            []string{testRoot + "a/b/c1", testRoot + "a/b/c2"}, paths)
        return
    }
}

func TestClient_CreateEphemeral(t *testing.T) {
    log.StdLog.SetLevel(log.LevelInfo)
    c := getClient(t)
    assert.Must(c != nil)

    key := testRoot + "key"
    val := "value"

    ch, err := c.CreateEphemeral(key, []byte(val))
    if err != nil {
        t.Errorf("create failed, detail: '%s'", err.Error())
        return
    }
    go func() {
        for !c.isClosed() {
            valRead, err := c.Read(key, true)
            if err != nil {
                if err.(*errors.TracedError).Cause != ErrClosedClient {
                    t.Errorf(err.Error())
                }
                return
            }
            if !bytes.Equal(valRead, []byte(val)) {
                t.Errorf("read failed, expect '%s', but met '%s'", val, string(valRead))
                return
            }
        }
    } ()

    ch2, err := c.CreateEphemeral(key, []byte(val))
    if err != ErrKeyAlreadyExists {
        t.Errorf("expect error '%s', but met '%v'", ErrKeyNotExists.Error(), err)
        return
    }
    assert.Must(ch2 == nil)

    time.Sleep(time.Second * 6)
    err = c.Close()
    if err != nil {
        t.Errorf(err.Error())
        return
    }
    <-ch
    // Sleep to make sure key are expired
    time.Sleep(c.timeout + 100 * time.Millisecond)

    c = getClientNonClearData(t)
    assert.Must(c != nil)

    _, err = c.Read(key, true)
    if err != ErrKeyNotExists {
        t.Errorf("expect key not exists")
        return
    }
}

func TestClient_CreateEphemeralInOrder(t *testing.T) {
    log.StdLog.SetLevel(log.LevelInfo)
    const threadNum = 5
    const key = testRoot + "key"
    const N = 100

    clients := make([]*Client, threadNum)
    for i := range clients {
        clients[i] = getClient(t)
        assert.Must(clients[i] != nil)
    }
    values := make([]string, threadNum)
    valueIndexMap := make(map[string]int)
    for i := range values {
        values[i] = string([]byte{'a' + byte(i)})
        valueIndexMap[values[i]] = i
    }

    err := clients[0].MkDir(key)
    if err != nil {
        t.Errorf("create dir '%s' failed", key)
        return
    }

    var wg sync.WaitGroup
    for i := 0; i < threadNum; i++ {
        wg.Add(1)

        go func(c *Client, val string) {
            defer wg.Done()

            for i := 0; i < N; i++ {
                _, _, err := c.CreateEphemeralInOrder(key, []byte(val))
                if err != nil {
                    t.Errorf(err.Error())
                    return
                }
            }
        }(clients[i], values[i])
    }
    wg.Wait()

    paths, err := clients[0].List(key, true)
    if err != nil {
        t.Errorf(err.Error())
        return
    }

    if len(paths) != N * threadNum {
        t.Errorf("paths length not match, expect %d, but met %d", N * threadNum, len(paths))
    }
    valCounts := make([]int, threadNum)
    for i := 0; i < N * threadNum; i++ {
        expect := fmt.Sprintf("%s/%09d", key, i)
        path := paths[i]
        if path != expect {
            t.Errorf("key not match, expect '%s', but met '%s'", expect, paths[i])
            continue
        }
        valRead, err := clients[0].ReadEphemeralInOrder(path, true)
        if err != nil {
            t.Errorf(err.Error())
            continue
        }

        if valIdx, ok := valueIndexMap[string(valRead)]; !ok {
            t.Errorf("unexpected value: '%s', expect in '%v'", string(valRead), values)
            continue
        } else {
            valCounts[valIdx]++
        }
    }

    for i := 0; i < threadNum; i++ {
        if valCounts[i] != N {
            t.Errorf("value count not match, exp %d, but met %d", N, valCounts[i])
        }
    }
}

func TestClient_WatchInOrder(t *testing.T) {
    log.StdLog.SetLevel(log.LevelInfo)
    const key = testRoot + "key"
    const N = 1000

    c1 := getClient(t)
    assert.Must(c1 != nil)
    c2 := getClient(t)
    assert.Must(c2 != nil)

    err := c1.MkDir(key)
    if err != nil {
        t.Errorf("create dir '%s' failed", key)
        return
    }

    watched := make(chan struct{})
    var wg sync.WaitGroup

    wg.Add(1)
    go func(c *Client, val string) {
        defer wg.Done()

        for i := 0; i < N; i++ {
            //fmt.Printf("[thread-1] round %d, waiting for signal from thread2\n", i)
            timeout := time.After(time.Second * 5)
            select {
            case _, ok := <-watched:
                if !ok {
                    return
                }
                break
            case <-timeout:
                t.Errorf("waiting for signal timeout")
                return
            }
            //fmt.Printf("[thread-1] round %d, received signal from thread2\n", i)

            _, _, err := c.CreateEphemeralInOrder(key, []byte(val))
            if err != nil {
                t.Errorf(err.Error())
                return
            }

            if i % 100 == 0 {
                fmt.Printf("%d rounds finished\n", i + 1)
            }
        }
        fmt.Printf("%d rounds finished\n", N)
    }(c1, "x")

    wg.Add(1)
    go func(c *Client) {
        defer wg.Done()
        defer close(watched)

        for i := 0; i < N; i++ {
            signal, _, err := c.WatchInOrder(key)
            if err != nil {
                t.Errorf(err.Error())
                return
            }

            //fmt.Printf("[thread-2] round %d, sending signal to wake up thread 1\n", i)
            timeout := time.After(time.Second * 5)
            select {
            case watched <-struct{}{}:
                break
            case <-timeout:
                t.Errorf("sending signal timeout")
                return
            }
            //fmt.Printf("[thread-2] round %d, sent signal to wake up thread 1\n", i)

            <-signal

            expKey := fmt.Sprintf("%s/%09d", key, i)
            maxKey, err := c.GetMaxKey(key)
            if err != nil {
                t.Errorf(err.Error())
                return
            }
            if maxKey != expKey {
                t.Errorf("key not match, expect '%s', but met '%s'", expKey, maxKey)
                return
            }
        }
    }(c2)

    wg.Wait()
}

func BenchmarkClient_Update(b *testing.B) {
    log.StdLog.SetLevel(log.LevelNone)
    c := getClient(b)
    assert.Must(c != nil)

    const key = testRoot + "key"
    const n = 10000
    start := time.Now()
    for i := 0; i < n; i++ {
        err := c.Update(key, []byte("xxxxxxxx"))
        if err != nil {
            b.Errorf(err.Error())
            return
        }
        if i % 100 == 0 {
            fmt.Printf("%d rounds finished\n", i+1)
        }
    }
    fmt.Printf("%d rounds finished\n", n)
    b.ReportMetric(float64(n)/(float64(time.Since(start))/float64(time.Second)), "qps")
}

func BenchmarkClient_Read(b *testing.B) {
    log.StdLog.SetLevel(log.LevelNone)
    c := getClient(b)
    assert.Must(c != nil)

    const key = testRoot + "key"
    err := c.Update(key, []byte("xxxxxxxx"))
    if err != nil {
        b.Errorf(err.Error())
        return
    }

    const n = 100000
    start := time.Now()
    for i := 0; i < n; i++ {
        valRead, err := c.Read(key, true)
        if err != nil {
            b.Errorf(err.Error())
            return
        }
        assert.Must(bytes.Equal(valRead, []byte("xxxxxxxx")))
        if i % 100 == 0 {
            fmt.Printf("%d rounds finished\n", i+1)
        }
    }
    b.ReportMetric(float64(n)/(float64(time.Since(start))/float64(time.Second)), "qps")
}

type DLock struct {
    Path    string
    Cli     *Client

    key     string
    counter int

    mu sync.Mutex
}

func NewDLock(path string, c *Client) *DLock {
    assert.Must(c != nil)
    return &DLock{Path:path, Cli:c}
}

func (dl *DLock) Lock() error {
    dl.mu.Lock()
    defer dl.mu.Unlock()

    if dl.counter == 0 {
        var err error
        _, dl.key, err = dl.Cli.CreateEphemeralInOrder(dl.Path, []byte{})
        if err != nil {
            return err
        }
    } else {
        assert.Must(dl.counter > 0)
        dl.counter++
        return nil
    }
    fmt.Printf("Locking '%s'\n", dl.key)

    for {
        minKey, err := dl.Cli.GetMinKey(dl.Path + "//")
        if err != nil {
           return err
        }
        if minKey == dl.key {
           dl.counter++
           return nil
        }
        ch, _, err := dl.Cli.WatchInOrder(dl.Path)
        if err != nil {
            return err
        }
        minKey, err = dl.Cli.GetMinKey(dl.Path + "//")
        if err != nil {
            return err
        }
        if minKey == dl.key {
            dl.counter++
            return nil
        }
        <-ch
    }
}

func (dl *DLock) Unlock() error {
    fmt.Printf("Unlocking '%s'\n", dl.key)
    dl.mu.Lock()
    defer dl.mu.Unlock()

    dl.counter--
    if dl.counter == 0 {
        err := dl.Cli.Delete(dl.key)
        if err != nil {
            return err
        }
        _, err = dl.Cli.c.Revoke(context.Background(), dl.Cli.leaseID)
        if err != nil {
            return err
        }
    }
    return nil
}

func TestClient_DistributedLock(t *testing.T)  {
    log.StdLog.SetLevel(log.LevelInfo)

    const threadNum = 5
    const N = 1000
    const lockPath = "/test/lock"
    num := int64(0)

    // clear data
    c := getClient(t)
    assert.Must(c != nil)
    err := c.MkDir(lockPath)
    if err != nil {
        t.Errorf(err.Error())
        return
    }

    var wg sync.WaitGroup
    for j := 0; j < threadNum; j++ {
        wg.Add(1)
        go func() {
            defer wg.Done()

            dl := NewDLock(lockPath, getClientNonClearDataWithTimeout(t, time.Second * 5))
            defer dl.Cli.Close()

            for i := 0; i < N; i++ {
                err := dl.Lock()
                if err != nil {
                   t.Errorf("locked failed, detail: '%s'", err.Error())
                   return
                }

                old := atomic.LoadInt64(&num)
                atomic.StoreInt64(&num, old + 1)

                err = dl.Unlock()
                if err != nil {
                   t.Errorf("locked failed, detai: '%s'", err.Error())
                   return
                }
            }
        }()
    }
    wg.Wait()
    if num != N * threadNum {
        t.Errorf("test failed, expect %d, but met %d", N * threadNum, num)
    }
}
