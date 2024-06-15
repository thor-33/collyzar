package main

import (
	"fmt"

	"github.com/Zartenc/collyzar/v2"
)

func main() {
	cs := &collyzar.CollyzarSettings{
		SpiderName: "zarten",
		Domain:     "www.amazon.com",
		RedisIp:    "172.104.15.85",
		RedisPW:    "cc_thor*&18",
	}

	collyzar.Run(myResponse, cs, nil)
}

func myResponse(response *collyzar.ZarResponse) {
	fmt.Println(response.StatusCode)
	fmt.Println(response.PushInfos)
}
