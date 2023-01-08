package main

import (
	"fmt"
	"time"

	"github.com/go-redis/redis/v8"
	"go.uber.org/zap"
)

// HashSet :redis hash-set
// see : http://redis.shibu.jp/commandreference/hashes.html
func HashSet(redisClient *redis.Client, key, field string, value interface{}) {
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
func HashMSet(redisClient *redis.Client, key, value string) {
	// Set
	logger.Debug("HashMSet.", zap.String("key:", key), zap.String("value:", value))
	err := redisClient.HMSet(ctx, key, value).Err()
	if err != nil {
		logger.Error("redis.Client.HMSet Error:", zap.Error(err))
	}
}

// HashGet :redis hash get
func HashGet(redisClient *redis.Client, key, field string) string {
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
func HashDelete(redisClient *redis.Client, key, field string) {
	logger.Debug("HashDelete.", zap.String("key:", key), zap.String("field:", field))
	err := redisClient.HDel(ctx, key, field).Err()

	if err != nil {
		logger.Error("redis.Client.HDel Error:", zap.Error(err))
	}
}

// Get : redis get
func Get(redisClient *redis.Client, key string) string {
	// Get
	logger.Debug("Get.", zap.String("key:", key))
	val, err := redisClient.Get(ctx, key).Result()

	if err != nil {
		logger.Error("redis.Client.Get Error:", zap.Error(err))
	}
	return val
}

// DELETE : redis delete
func DELETE(redisClient *redis.Client, key string) {
	logger.Debug("DELETE.", zap.String("key:", key))
	err := redisClient.Del(ctx, key).Err()

	if err != nil {
		logger.Error("redis.Client.Del Error:", zap.Error(err))
	}
}

// ZAdd : redis zadd
func ZAdd(redisClient *redis.Client, key string, z *redis.Z) {
	logger.Debug("ZAdd.", zap.String("key:", key), zap.Any("z:", z))
	err := redisClient.ZAdd(ctx, key, z).Err()
	if err != nil {
		logger.Error("redis.Client.ZAdd Error:", zap.Error(err))
	}
}

// SetNX : redis setnx
func SetNX(redisClient *redis.Client, key, value string) bool {
	logger.Debug("SetNX.", zap.String("key:", key), zap.String("value:", value))
	res, err := redisClient.SetNX(ctx, key, value, time.Hour).Result()
	if err != nil {
		logger.Error("redis.Client.SetNX Error:", zap.Error(err))
	}
	fmt.Println("SetNX res : ", res)
	return res
}
