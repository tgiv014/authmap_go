package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"regexp"
	"time"

	"github.com/hpcloud/tail"
	influxdb2 "github.com/influxdata/influxdb-client-go/v2"
	"github.com/oschwald/geoip2-golang"
)

var (
	reSSHD    *regexp.Regexp
	reLogline *regexp.Regexp
	reIP      *regexp.Regexp
	reTags    map[string]*regexp.Regexp = make(map[string]*regexp.Regexp)
)

func init() {
	var err error
	reSSHD, err = regexp.Compile(`sshd\[[0-9]+\]:`)
	if err != nil {
		log.Fatal(err)
	}
	reLogline, err = regexp.Compile(`sshd\[[0-9]+\]: +(.+)$`)
	if err != nil {
		log.Fatal(err)
	}
	reTags["accepted"], err = regexp.Compile(`^Accepted.+$`)
	if err != nil {
		log.Fatal(err)
	}
	reTags["good_disconnect"], err = regexp.Compile(`^Disconnected from user.+$`)
	if err != nil {
		log.Fatal(err)
	}
	reTags["bad_disconnect"], err = regexp.Compile(`^Disconnected from.+$`)
	if err != nil {
		log.Fatal(err)
	}
	reTags["invalid_user"], err = regexp.Compile(`^Connection closed|reset by invalid user.+$`)
	if err != nil {
		log.Fatal(err)
	}
	reIP, err = regexp.Compile(`([0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3})`)
	if err != nil {
		log.Fatal(err)
	}
}

func main() {
	db, err := geoip2.Open("/etc/authmap/GeoLite2-City.mmdb")
	if err != nil {
		// Attempt to open a local copy if present
		db, err = geoip2.Open("./GeoLite2-City.mmdb")
		if err != nil {
			log.Fatal("Error opening GeoLite database", err)
		}
	}
	defer db.Close()

	client := influxdb2.NewClient("http://influxdb:8086", "my-token")
	writeAPI := client.WriteAPIBlocking("yeet.retweet", "db0")

	start := time.Now()
	if _, err := os.Stat("/var/log/auth.log"); os.IsNotExist(err) {
		log.Fatal("Could not find auth.log. If running in docker, make sure you have bound it")
	}
	// This will tail the file indefinitely
	t, err := tail.TailFile("/var/log/auth.log", tail.Config{ReOpen: true, MustExist: true, Follow: true, Logger: tail.DiscardingLogger})
	for line := range t.Lines {
		// A little hacky... but it forces us to wait for new logs
		if line.Time.Sub(start) > 5*time.Second {
			// Ensure logs are from sshd
			if reSSHD.Find([]byte(line.Text)) == nil {
				continue
			}
			logline := reLogline.FindSubmatch([]byte(line.Text))[1]
			if logline == nil {
				continue
			}
			var lineTag string = ""
			for tag, re := range reTags {
				if re.Find(logline) != nil {
					lineTag = tag
				}
			}
			if lineTag == "" {
				continue
			}
			ipStr := string(reIP.Find(logline))
			fmt.Println(lineTag, string(ipStr))
			ip := net.ParseIP(ipStr)
			if ip == nil {
				continue
			}
			record, err := db.City(ip)
			if err != nil {
				log.Fatal(err)
			}
			fmt.Println(record.Country.Names["en"])
			fmt.Println(record.Location.Latitude)
			fmt.Println(record.Location.Longitude)
			p := influxdb2.NewPointWithMeasurement("authattempt").
				AddTag("type", lineTag).
				AddField("country", record.Country.Names["en"]).
				AddField("lat", record.Location.Latitude).
				AddField("lon", record.Location.Longitude).
				AddField("ip", ipStr).
				SetTime(time.Now())
			writeAPI.WritePoint(context.Background(), p)
		}
	}
}
