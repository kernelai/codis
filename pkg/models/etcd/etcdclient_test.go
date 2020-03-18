package etcdclient

import (
    "bytes"
    "fmt"
    "github.com/CodisLabs/codis/pkg/utils/assert"
    "github.com/CodisLabs/codis/pkg/utils/log"
    "github.com/coreos/etcd/client"
    "os"
    "testing"
    "time"
)

const testRoot = "/test/"

func getClient(t testing.TB) *Client {
    etcdAddr := os.Getenv("ETCD_TEST_SERVER_ADDR")
    if len(etcdAddr) == 0 {
        t.Errorf("etcd server addr not found, you must set env ETCD_TEST_SERVER_ADDR first")
        return nil
    }
    c, err := New(etcdAddr, "", time.Second*5)
    if err != nil {
        t.Errorf("can't connection to %s, detail: '%s'", etcdAddr, err.Error())
        return nil
    }

    cntx, cancel := c.newContext()
    defer cancel()
    t.Logf("etcd delete dir %s", testRoot)
    _, err = c.kapi.Delete(cntx, testRoot, &client.DeleteOptions{Recursive:true})
    if err != nil && !isErrNoNode(err) {
        t.Errorf("etcd delete dir '%s' failed: %s", testRoot, err)
        return nil
    }
    t.Logf("test dir '%s' cleared before testing", testRoot)
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
    if err == nil {
        t.Errorf("expect error 'key already exists' while creating key '%s', but met '%v'", key, err)
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
    if err == nil {
        t.Errorf("expect error '%s', but met '%v'", "key not exists", err)
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
    if err == nil {
        t.Errorf("expected error key not exists")
        return
    }

    key2 := keyspace2 + "key2"
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
    if len(keys) != 3 {
        t.Errorf("list value length not correct, exp 2, but met %d", len(keys))
        return
    }
    if !(keys[0] == keyspace1 && keys[1] == keyspace2 && keys[2] == key2) {
        t.Errorf("list values not correct, exp %v, but met %v", []string{keyspace1, keyspace2}, keys)
        return
    }

    keys, err = c.List(testRoot+"xxx", true)
    if err == nil {
        t.Errorf("can't list key '%s', detail: '%v'", testRoot+"xxx", err)
        return
    }
    keys, err = c.List(testRoot+"xxx", false)
    if err != nil {
        t.Errorf("expect no error if must is false")
        return
    }
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
