// Package redisclient keeps Redis implementation details behind a small
// internal boundary for packages that should depend on a client capability,
// not on the Redis module directly.
package redisclient

import "github.com/redis/go-redis/v9"

type Client = redis.Client
type Options = redis.Options
type Pipeliner = redis.Pipeliner
type Script = redis.Script
type Z = redis.Z

var Nil = redis.Nil

func NewScript(source string) *Script {
	return redis.NewScript(source)
}

func NewClient(options *Options) *Client {
	return redis.NewClient(options)
}
