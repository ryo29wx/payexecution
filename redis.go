package main

import (
	"fmt"
	"time"

	"github.com/go-redis/redis/v8"
	"go.uber.org/zap"
)

// Redis Class
type redisObj struct {
	redisClient *redis.Client
}

func newRedis(redisServerName, password string, db int) redisManager {
	// go-redis init
	redisClient := redis.NewClient(&redis.Options{
		Addr:     redisServerName,
		Password: password, // no password set
		DB:       db,  // use default DB
	})
	r := &redisObj{redisClient}
	return r
}

type redisManager interface {
	HashSet(*redis.Client, string, string, interface{})
	HashMSet(*redis.Client, string, string)
	HashGet(*redis.Client, string, string,) string
	HashDelete(*redis.Client, string, string)
	Get(*redis.Client, string) string
	DELETE(*redis.Client, string)
	ZAdd(*redis.Client, string, *redis.Z)
	SetNX(*redis.Client, string, string) bool 
}


// HashSet :redis hash-set
// see : http://redis.shibu.jp/commandreference/hashes.html
func (r *redisObj) HashSet(redisClient *redis.Client, key, field string, value interface{}) {
	logger.Debug("HashSet.",
		zap.String("key:", key),
		zap.String("field:", field),
		zap.Any("value:", value))
	err := redisClient.HSet(ctx, key, field, value).Err()
	if err != nil {
		logger.Error("redis.Client.HSet Error:", zap.Error(err))
	}
}

// HashMSet :redis hash malti set
func (r *redisObj) HashMSet(redisClient *redis.Client, key, value string) {
	// Set
	logger.Debug("HashMSet.", zap.String("key:", key), zap.String("value:", value))
	err := redisClient.HMSet(ctx, key, value).Err()
	if err != nil {
		logger.Error("redis.Client.HMSet Error:", zap.Error(err))
	}
}

// HashGet :redis hash get
func (r *redisObj) HashGet(redisClient *redis.Client, key, field string) string {
	// Get
	// HGet(key, field string) *StringCmd
	logger.Debug("HashGet.", zap.String("key:", key), zap.String("field:", field))

	hgetVal, err := redisClient.HGet(ctx, key, field).Result()
	if err != nil {
		logger.Error("redis.Client.HGet Error:", zap.Error(err))
	}

	return hgetVal
}

// HashDelete : redis hash delete
func (r *redisObj) HashDelete(redisClient *redis.Client, key, field string) {
	logger.Debug("HashDelete.", zap.String("key:", key), zap.String("field:", field))
	err := redisClient.HDel(ctx, key, field).Err()

	if err != nil {
		logger.Error("redis.Client.HDel Error:", zap.Error(err))
	}
}

// Get : redis get
func (r *redisObj) Get(redisClient *redis.Client, key string) string {
	// Get
	logger.Debug("Get.", zap.String("key:", key))
	val, err := redisClient.Get(ctx, key).Result()

	if err != nil {
		logger.Error("redis.Client.Get Error:", zap.Error(err))
	}
	return val
}

// DELETE : redis delete
func (r *redisObj) DELETE(redisClient *redis.Client, key string) {
	logger.Debug("DELETE.", zap.String("key:", key))
	err := redisClient.Del(ctx, key).Err()

	if err != nil {
		logger.Error("redis.Client.Del Error:", zap.Error(err))
	}
}

// ZAdd : redis zadd
func (r *redisObj) ZAdd(redisClient *redis.Client, key string, z *redis.Z) {
	logger.Debug("ZAdd.", zap.String("key:", key), zap.Any("z:", z))
	err := redisClient.ZAdd(ctx, key, z).Err()
	if err != nil {
		logger.Error("redis.Client.ZAdd Error:", zap.Error(err))
	}
}

// SetNX : redis setnx
func (r *redisObj) SetNX(redisClient *redis.Client, key, value string) bool {
	logger.Debug("SetNX.", zap.String("key:", key), zap.String("value:", value))
	res, err := redisClient.SetNX(ctx, key, value, time.Hour).Result()
	if err != nil {
		logger.Error("redis.Client.SetNX Error:", zap.Error(err))
	}
	fmt.Println("SetNX res : ", res)
	return res
}
