package main

import (
	"context"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"os"
	"regexp"
	"time"

	"github.com/hpcloud/tail"
	influxdb2 "github.com/influxdata/influxdb-client-go/v2"
	"github.com/oschwald/geoip2-golang"
	"gopkg.in/yaml.v2"
)

type InfluxConfig struct {
	URL   string `yaml:"url"`
	Token string `yaml:"token"`
	Org   string `yaml:"org"`
	DB    string `yaml:"db"`
}
type GeoLiteConfig struct {
	Path string `yaml:"path"`
}
type AuthConfig struct {
	Path       string `yaml:"path"`
	WaitLength string `yaml:"wait_length"`
}
type Config struct {
	Influx  InfluxConfig  `yaml:"influx"`
	GeoLite GeoLiteConfig `yaml:"geolite"`
	Auth    AuthConfig    `yaml:"auth"`
}

const ConfigPath string = "/etc/authmap/config.yaml"

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

func load_config() Config {
	config := Config{
		Influx: InfluxConfig{
			URL:   "http://influxdb:8086",
			Token: "",
			Org:   "",
			DB:    "db0",
		},
		GeoLite: GeoLiteConfig{
			Path: "/etc/authmap/GeoLite2-City.mmdb",
		},
		Auth: AuthConfig{
			Path:       "/var/log/auth.log",
			WaitLength: "5s",
		},
	}
	if _, err := os.Stat(ConfigPath); os.IsNotExist(err) {
		// Configuration does not exists... Let's generate one
		fmt.Println("Generating a default configuration")
		cb, err := yaml.Marshal(&config)
		if err != nil {
			log.Fatal("Unable to marshall default config.")
		}
		f, err := os.Create(ConfigPath)
		if err != nil {
			log.Fatal("Unable to open new config file")
		}
		_, err = f.Write(cb)
		if err != nil {
			log.Fatal("Unable to write to new config file")
		}
		err = f.Close()
		if err != nil {
			log.Fatal("Encountered error while closing config file")
		}

	}
	f, err := os.Open(ConfigPath)
	if err != nil {
		log.Fatal(err)
	}
	b, err := ioutil.ReadAll(f)
	if err != nil {
		log.Fatal(err)
	}
	err = f.Close()
	if err != nil {
		log.Fatal("Encountered error while closing config file")
	}

	err = yaml.Unmarshal([]byte(b), &config)
	if err != nil {
		log.Fatalf("error: %v", err)
	}
	return config
}

func main() {
	config := load_config()
	db, err := geoip2.Open(config.GeoLite.Path)
	if err != nil {
		log.Fatal("Error opening GeoLite database", err)
	}
	defer db.Close()

	client := influxdb2.NewClient(config.Influx.URL, config.Influx.Token)
	writeAPI := client.WriteAPIBlocking(config.Influx.Org, config.Influx.DB)

	start := time.Now()
	if _, err := os.Stat(config.Auth.Path); os.IsNotExist(err) {
		log.Fatal("Could not find auth.log. If running in docker, make sure you have bound it")
	}
	waitDuration, err := time.ParseDuration(config.Auth.WaitLength)
	if err != nil {
		log.Fatal("Could not parse duration")
	}
	// This will tail the file indefinitely
	t, err := tail.TailFile(config.Auth.Path, tail.Config{ReOpen: true, MustExist: true, Follow: true, Logger: tail.DiscardingLogger})
	for line := range t.Lines {
		// A little hacky... but it forces us to wait for new logs
		if line.Time.Sub(start) > waitDuration {
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
				AddTag("ip", ipStr).
				AddField("country", record.Country.Names["en"]).
				AddField("lat", record.Location.Latitude).
				AddField("lon", record.Location.Longitude).
				SetTime(time.Now())
			writeAPI.WritePoint(context.Background(), p)
		}
	}
}
