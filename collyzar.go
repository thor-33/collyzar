package collyzar

import (
	"crypto/tls"
	"encoding/json"
	"github.com/gomodule/redigo/redis"
	"github.com/gocolly/colly/v2/proxy"
	"github.com/gocolly/colly/v2"
	"github.com/gocolly/colly/v2/extensions"
	log "github.com/sirupsen/logrus"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"time"
	"gopkg.in/bculberson/bloom.v2"

)
type ZarResponse struct {
	StatusCode int
	Body []byte
	Headers *http.Header
	Request *ZarRequest
	PushInfos PushInfo
}

type ZarRequest struct {
	URL *url.URL
	Headers *http.Header
	Method string
	ProxyURL string
}

type Callback func(*ZarResponse)

var gSpiderStatus = "0" //0:running 1:pause 2:stop

var (
	spiderName string
	domain string
	redisIp string
	redisPort int //default 6379
	redisPW string
	referer string
	cookie string
	proxyUrls []string
	concurrentRequest int //default 20
	downloadTimeout int //default 120
	randomDelay int //default 0
	disableCookies bool //fefault true
	isRetry bool //default true
	retryTimes int //default 3
)

func (zar *ZarResponse) RequestNew(newPushInfos PushInfo) error{
	ts := NewToolSpider(redisIp, redisPort, redisPW, spiderName)
	err := ts.PushToQueue(newPushInfos)
	if err != nil{
		return err
	}
	return nil
}

func initParam(cs *CollyzarSettings, ss *SpiderSettings){
	if cs == nil{
		log.WithFields(log.Fields{
			"collyzar": "must settings error",
		}).Fatalln("CollyzarSettings must be initialized")
		return
	}
	if ss == nil{
		ss = &SpiderSettings{}
	}
	spiderName = cs.SpiderName
	domain = cs.Domain
	redisIp = cs.RedisIp
	redisPort = cs.RedisPort //default 6379
	redisPW = cs.RedisPW

	referer = ss.Referer
	cookie = ss.Cookie
	proxyUrls = ss.ProxyUrls
	concurrentRequest = ss.ConcurrentRequest //default 20
	downloadTimeout = ss.DownloadTimeout //default 120
	randomDelay = ss.RandomDelay //default 0
	disableCookies = ss.DisableCookies //default true
	isRetry = ss.IsRetry //default true
	retryTimes = ss.RetryTimes //default 3

	if redisPort == 0{
		redisPort = 6379
	}
	if concurrentRequest == 0{
		concurrentRequest = 20
	}
	if downloadTimeout == 0{
		downloadTimeout = 120
	}
	if randomDelay == 0{
		randomDelay = 0
	}
	if disableCookies == false{
		disableCookies = true
	}
	if isRetry == false{
		isRetry = true
	}
	if retryTimes == 0{
		retryTimes = 3
	}

}

func Run(callback Callback, cs *CollyzarSettings, ss *SpiderSettings){
	initParam(cs, ss)
	if spiderName == "" || domain == "" || redisIp == ""{
		log.WithFields(log.Fields{
			"collyzar": "settings error",
		}).Fatalln("must configure must-settings")

	}
	var err error

	c := colly.NewCollector(
		colly.AllowedDomains(domain),
		colly.Async(true),
		colly.AllowURLRevisit(),
		colly.IgnoreRobotsTxt(),
	)
	err = c.Limit(&colly.LimitRule{
		DomainGlob: domain,
		RandomDelay:  time.Duration(randomDelay) * time.Second,
		Parallelism: concurrentRequest,
	})
	if err != nil{
		log.WithFields(log.Fields{
			"collyzar": "limit rule errir",
		}).Error(err)

	}

	c.WithTransport(&http.Transport{
		DisableKeepAlives: true,
		TLSClientConfig:&tls.Config{InsecureSkipVerify: true},
	})

	c.SetRequestTimeout(time.Second * time.Duration(downloadTimeout))

	if disableCookies{
		c.DisableCookies()
	}

	extensions.RandomUserAgent(c) //desktop ua

	if len(proxyUrls) >0 {
		rp, err := proxy.RoundRobinProxySwitcher(proxyUrls...)
		if err != nil {
			log.WithFields(log.Fields{
				"collyzar": "set proxy switch error",
			}).Fatal(err)
		}
		c.SetProxyFunc(rp)
	}

	c.OnRequest(func(r *colly.Request) {
		r.Headers.Set("Referer", referer)
		r.Headers.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
		r.Headers.Set("Accept-Language", "zh-CN,zh;q=0.8,zh-TW;q=0.7,zh-HK;q=0.5,en-US;q=0.3,en;q=0.2")
		r.Headers.Set("Cookie", cookie)

	})

	c.OnError(func(r *colly.Response, err error) {
		log.WithFields(log.Fields{
			"collyzar": "problem with download",
		}).Debug(err)

		if isRetry{
			err = r.Request.Retry()
			if err != nil{
				log.WithFields(log.Fields{
					"collyzar": "retry visit error",
				}).Error(err)
			}
		}
	})

	c.OnResponse(func(r *colly.Response){
		statusCode := r.StatusCode
		retryHttpCodes := []int{500, 502, 503, 504, 522, 524, 408, 429}
		isRetryCode := false
		for _, s := range retryHttpCodes{
			if statusCode == s{
				isRetryCode = true
				break
			}
		}
		if isRetry && isRetryCode{
			var reqTimes int
			reqTimesIf := r.Ctx.GetAny("req_times")
			if reqTimesIf == nil{
				reqTimes = 1
				r.Request.Ctx.Put("req_times", reqTimes)
			}else {
				reqTimes = reqTimesIf.(int)
			}

			if reqTimes <= retryTimes{
				reqTimes += 1
				r.Request.Ctx.Put("req_times", reqTimes)
				err = r.Request.Retry()
				if err != nil{
					log.WithFields(log.Fields{
						"collyzar": "OnResponse retry visit Error",
					}).Error(err)
				}
			}
		}else {
			zarReq := new(ZarRequest)
			zarReq.URL = r.Request.URL
			zarReq.Headers = r.Request.Headers
			zarReq.Method = r.Request.Method
			zarReq.ProxyURL = r.Request.ProxyURL

			zarRes := new(ZarResponse)
			zarRes.StatusCode = r.StatusCode
			zarRes.Body = r.Body
			zarRes.Headers = r.Headers
			zarRes.Request = zarReq
			zarRes.PushInfos = r.Ctx.GetAny("url_info").(PushInfo)

			callback(zarRes)
			}

	})

	spiderQueue(c)
	c.Wait()

}

func spiderQueue(c *colly.Collector){
	pool := &redis.Pool{
		MaxActive:7000,
		MaxIdle:2000,
		Wait:true,
		Dial:func() (redis.Conn, error) {
			conn, err := redis.Dial("tcp", redisIp + ":" + strconv.Itoa(redisPort),
				redis.DialPassword(redisPW),
				redis.DialDatabase(1),)
			if err != nil {
				log.WithFields(log.Fields{
					"collyzar": "connect redis pool error",
				}).Fatalln(err)
				return nil, err
			}
			return conn, nil
		},
	}

	setSpiderSignal(pool, spiderName)
	go detectSpiderSignal(pool, spiderName)

	for{
		isStop := spiderWatch(pool, c)
		if isStop{
			break
		}
		pauseTime := rand.Intn(1000) //0-1000
		time.Sleep(time.Millisecond * time.Duration(pauseTime))
	}
	cleanRedis(pool)

}

func isGlobReuested(bf *bloom.BloomFilter, url string) bool {
	isExist, err := bf.Exists([]byte(url))
	if err != nil{
		log.WithFields(log.Fields{
			"collyzar": "bloom judge exist error",
		}).Error(err)
	}
	if !isExist{
		err := bf.Add([]byte(url))
		if err != nil{
			log.WithFields(log.Fields{
				"collyzar": "bloom add error",
			}).Error(err)
		}
	}
	return isExist
}

func detectSpiderSignal(pool *redis.Pool, spiderId string){
	for{
		func(){
			rdb := pool.Get()
			defer rdb.Close()
			stopStatusI, err := rdb.Do("HGET","collyzar_spider_status", spiderId)
			stopStatus := string(stopStatusI.([]byte))
			if err != nil{
				log.WithFields(log.Fields{
					"collyzar": "get redis status error",
				}).Error(err)
			}
			//pause
			if stopStatus == "1"{
				gSpiderStatus = "1"
			}
			//stop
			if stopStatus == "2"{
				gSpiderStatus = "2"
				return
			}
		}()
		time.Sleep(time.Second * 2)
	}
}

func setSpiderSignal(pool *redis.Pool, spiderId string){
	rdb := pool.Get()
	defer rdb.Close()

	_, err := rdb.Do("HSET", "collyzar_spider_status", spiderId, gSpiderStatus)
	if err != nil{
		log.WithFields(log.Fields{
			"collyzar": "set spider signal error",
		}).Error(err)
	}
}

func genBloomFilter(rdb redis.Conn) *bloom.BloomFilter{
	bfName := spiderName + "BF"
	m, k := bloom.EstimateParameters(8*1024*1024 * 200, .01)
	bitSet := bloom.NewRedisBitSet(bfName, m, rdb)
	bf := bloom.New(m, k, bitSet)
	return bf
}

func cleanRedis(pool *redis.Pool){
	rdb := pool.Get()
	defer rdb.Close()

	r, err := rdb.Do("keys", "zartenBF:*")
	if err != nil{
		log.WithFields(log.Fields{
			"collyzar": "del redis bloom queue error",
		}).Error(err)
	}
	rnames := r.([]interface {})
	for _,rn := range rnames{
		_, err := rdb.Do("DEL", string(rn.([]byte)))
		if err != nil{
			log.WithFields(log.Fields{
				"collyzar": "del redis bloom queue error",
			}).Error(err)

		}

	_, err = rdb.Do("HDEL", "collyzar_spider_status", spiderName)
	if err != nil{
		log.WithFields(log.Fields{
			"collyzar": "del redis bloom queue error",
		}).Error(err)}
	}
}

func spiderWatch(pool *redis.Pool, c *colly.Collector) bool {
	rdb := pool.Get()
	defer rdb.Close()

	globBF := genBloomFilter(rdb)

	if gSpiderStatus == "1"{
		log.WithFields(log.Fields{
			"collyzar": "spider status",
		}).Info("pause spider")
	}else if gSpiderStatus == "2"{
		log.WithFields(log.Fields{
			"collyzar": "stop spider",
		}).Info("Wait for spider request finish and then stop")
		return true
	}

	if gSpiderStatus != "0"{
		return  false
	}
	urlInfoI, err := rdb.Do("RPOP", spiderName)
	if err != nil{
		log.WithFields(log.Fields{
			"collyzar": "get redis spider queue error",
		}).Error(err)
	}
	if urlInfoI == nil{
		time.Sleep(time.Second * 1)
		return false
	}

	urlInfo := string(urlInfoI.([]byte))
	var oUrlInfo PushInfo
	err = json.Unmarshal([]byte(urlInfo), &oUrlInfo)
	if err != nil{
		log.WithFields(log.Fields{
			"collyzar": "redis push info serialize error",
		}).Error(err)
	}

	isV, err := c.HasVisited(oUrlInfo.Url)
	if err != nil{
		log.WithFields(log.Fields{
			"collyzar": "judge has visited error",
		}).Error(err)
	}
	if !isV{
		if !isGlobReuested(globBF, oUrlInfo.Url){
			ctx := colly.NewContext()
			ctx.Put("url_info", oUrlInfo)
			err = c.Request("GET", oUrlInfo.Url, nil, ctx, nil)
			if err != nil{
				log.WithFields(log.Fields{
					"collyzar": "visit url error",
				}).Error(err)
			}
		}
	}
	return false
}

