package redis

import (
	"github.com/go-redis/redis"
	"istio.io/pkg/log"
	"os"
	"strconv"
)

var client *redis.Client

func GetUsedUnits(applicationId string) int {
	val, err := getRedisClient().HGet(applicationId, "used").Result()
	if err == redis.Nil {
		return 0
	}
	units, parseErr := strconv.Atoi(val)
	if parseErr != nil {
		log.Fatalf(parseErr.Error())
	}
	return units
}

func GetRemainingUnits(applicationId string) int {
	val, err := getRedisClient().HGet(applicationId, "remaining").Result()
	if err == redis.Nil {
		return 0
	}
	units, parseErr := strconv.Atoi(val)
	if parseErr != nil {
		log.Fatalf(parseErr.Error())
	}
	return units
}

func SetUsedUnits(applicationId string, used int) {
	_, err := getRedisClient().HSet(applicationId, "used", used).Result()
	if err != nil {
		log.Errorf(err.Error())
	}
}

func SetRemainingUnits(applicationId string, remaining int) {
	_, err := getRedisClient().HSet(applicationId, "remaining", remaining).Result()
	if err != nil {
		log.Errorf(err.Error())
	}
}

func GetUsageInfo() map[string]int {
	resultMap := make(map[string]int)
	keys, _, err := getRedisClient().Scan(0, "*", 0).Result()
	if err != nil {
		log.Errorf(err.Error())
	}
	for _, key := range keys {
		val := GetUsedUnits(key)
		resultMap[key] = val
	}
	return resultMap
}

func IncreaseUsedValue(applicationId string) {
	val := GetUsedUnits(applicationId)
	SetUsedUnits(applicationId, val+1)
}

func DecreaseRemainingValue(applicationId string) {
	val := GetRemainingUnits(applicationId)
	SetRemainingUnits(applicationId, val-1)
}

func getRedisClient() *redis.Client {
	if client != nil {
		return client
	}

	var (
		host     = getEnv("REDIS_HOST", "localhost")
		port     = getEnv("REDIS_PORT", "6379")
		password = getEnv("REDIS_PASSWORD", "")
	)

	client = redis.NewClient(&redis.Options{
		Addr:     host + ":" + port,
		Password: password,
		DB:       0,
	})

	_, err := client.Ping().Result()
	if err != nil {
		log.Fatalf(err.Error())
	}

	return client
}

func getEnv(key, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
}
