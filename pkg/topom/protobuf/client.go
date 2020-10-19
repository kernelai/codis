// Copyright 2016 CodisLabs. All Rights Reserved.
// Licensed under the MIT (MIT-LICENSE.txt) license.

package protobuf

import (
	"time"
)

type Client struct {
	*Conn
}

//TODO  config
func NewClient(addr string) (*Client, error) {
	c, err := DialTimeout(addr, time.Second*5,
		128000,
		128000)
	if err != nil {
		return nil, err
	}
	c.ReaderTimeout = time.Second * 30
	c.WriterTimeout = time.Second * 30
	c.SetKeepAlivePeriod(time.Second * 5)
	client := &Client{Conn: c}
	return client, nil
}

func (s *Client) Call() error {
	c := s.Conn
	return nil
}

func (s *Client) send() error {
	return nil
}
