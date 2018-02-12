package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"time"

	"github.com/yosssi/gmq/mqtt"
	"github.com/yosssi/gmq/mqtt/client"
	"periph.io/x/periph/conn/spi"
	"periph.io/x/periph/conn/spi/spireg"
	"periph.io/x/periph/host"
)

type Configuration struct {
	BasicVerbose bool   `json:"basicVerbose"`
	BasicLogger  bool   `json:"basicLogger"`
	BasicTimer   int    `json:"basicTimer"`
	MqttAddress  string `json:"mqttAddress"`
	MqttTopic    string `json:"mqttTopic"`
}

var configuration Configuration
var beforeValuesBool map[string]bool

func getMCP3008Value(s spi.Conn, command byte) (float64, error) {
	hex := false
	var write []byte
	var err error

	hex = true
	write = append(write, byte(1))
	write = append(write, byte(command))
	write = append(write, byte(0x00))
	ss := 0.0
	read := make([]byte, len(write))
	if err = s.Tx(write, read); err != nil {
		return ss, err
	}

	if !hex {
		_, err = os.Stdout.Write(read)
	} else {
		var num int = int(byte((read[1])&byte(0x03))) * 256
		num += int(byte(read[2]) & byte(0xFF))
		nam := float64(num)
		ss = nam / 1024 * 4.574
	}
	return ss, err
}

// runTx does the I/O.
//
// If you find yourself with the need to do a one-off complicated transaction
// using TxPackets, temporarily override this function.
func runTx(s spi.Conn, logger bool, cli *client.Client) {

	commands := map[byte]float64{0x80: 1.0, 0x90: 1.0, 0xA0: 1.0, 0xB0: 1.25, 0xC0: 13, 0xD0: 6.0, 0xE0: 1.6667, 0xF0: 1.6667}

	var values map[string]float64
	values = make(map[string]float64)

	var valuesBool map[string]bool
	valuesBool = make(map[string]bool)

	for command := byte(0x80); command >= 0x80 && command <= 0xF0; command += 0x10 {
		multip, _ := commands[command]
		tempfloat, err := getMCP3008Value(s, command)

		if err != nil {
			fmt.Fprintf(os.Stderr, "spi-io: %s.\n", err)
			os.Exit(1)
		}
		switch command {
		case 0x80:
			values["1V8"] = tempfloat * multip
			if 1.7 <= values["1V8"] && values["1V8"] <= 1.9 {
				valuesBool["1V8"] = true
			} else {
				valuesBool["1V8"] = false
			}
		case 0x90:
			values["3V3"] = tempfloat * multip
			if 3.1 <= values["3V3"] && values["3V3"] <= 3.5 {
				valuesBool["3V3"] = true
			} else {
				valuesBool["3V3"] = false
			}
		case 0xA0:
			values["batteryV"] = tempfloat * multip
			if 3.5 <= values["batteryV"] && values["batteryV"] <= 4.3 {
				valuesBool["batteryV"] = true
			} else {
				valuesBool["batteryV"] = false
			}
		case 0xB0:
			values["5V"] = tempfloat * multip
			if 4.7 <= values["5V"] && values["5V"] <= 5.3 {
				valuesBool["5V"] = true
			} else {
				valuesBool["5V"] = false
			}

		case 0xC0:
			values["48V"] = tempfloat*multip + 0.7
			if 45 <= values["48V"] && values["48V"] <= 50 {
				valuesBool["48V"] = true
			} else {
				valuesBool["48V"] = false
			}
		case 0xD0:
			values["solarV"] = tempfloat*multip + 0.8
		case 0xE0:
			values["batteryI"] = (tempfloat - 1.8) * multip
		case 0xF0:
			if _, ok := values["solarV"]; ok {
				values["solarI"] = (tempfloat - 1.8) * multip * 4.13 / values["solarV"]
			}
		default:
			fmt.Fprintf(os.Stderr, "case_error\n")
			os.Exit(1)
		}
	}
	for name, bol := range valuesBool {
		if beforeValuesBool[name] != bol {
			str := "off"
			if !beforeValuesBool[name] && bol {
				str = "on"
			}
			err := cli.Publish(&client.PublishOptions{
				QoS:       mqtt.QoS0,
				TopicName: []byte("log"),
				Message:   []byte(name + ":" + str),
			})
			if err != nil {
				panic(err)
			}

		}

	}
	beforeValuesBool = valuesBool
	if logger {
		fmt.Println(values)
	}
	jsonString, _ := json.Marshal(values)
	// Publish a message.
	err := cli.Publish(&client.PublishOptions{
		QoS:       mqtt.QoS0,
		TopicName: []byte(configuration.MqttTopic),
		Message:   []byte(jsonString),
	})
	if err != nil {
		panic(err)
	}

	return

}

func main() {

	beforeValuesBool = make(map[string]bool)

	dat, _ := ioutil.ReadFile("conf.json")
	decoder := json.NewDecoder(bytes.NewBufferString(string(dat)))
	fmt.Println(string(dat))
	err := decoder.Decode(&configuration)

	if err != nil {
		fmt.Println("error:", err)
	}
	fmt.Println(configuration)

	// Create an MQTT Client.
	cli := client.New(&client.Options{
		// Define the processing of the error handler.
		ErrorHandler: func(err error) {
			fmt.Println(err)
		},
	})
	// Terminate the Client.
	defer cli.Terminate()

	// Connect to the MQTT Server.
	err = cli.Connect(&client.ConnectOptions{
		Network:  "tcp",
		Address:  configuration.MqttAddress,
		ClientID: []byte("rpi-client"),
	})
	if err != nil {
		panic(err)
	}

	hz := 1000000
	bits := 8
	verbose := flag.Bool("v", configuration.BasicVerbose, "verbose mode")
	logger := flag.Bool("l", configuration.BasicLogger, "logger mode")
	timerIntPtr := flag.Int("t", configuration.BasicTimer, "an int")

	flag.Parse()

	timerInt := *timerIntPtr
	if !*verbose {
		log.SetOutput(ioutil.Discard)
	}
	log.SetFlags(log.Lmicroseconds)

	m := spi.Mode(0)

	if _, err := host.Init(); err != nil {
		fmt.Fprintf(os.Stderr, "init_error: %s.\n", err)
		os.Exit(1)
	}
	s, err := spireg.Open("SPI1.0")
	if err != nil {
		fmt.Fprintf(os.Stderr, "open_error: %s.\n", err)
		os.Exit(1)
	}
	defer s.Close()
	c, err := s.Connect(int64(hz), m, bits)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect_error: %s.\n", err)
		os.Exit(1)
	}
	if *verbose {
		if p, ok := c.(spi.Pins); ok {
			log.Printf("Using pins CLK: %s  MOSI: %s  MISO:  %s  CS:  %s\r\n", p.CLK(), p.MOSI(), p.MISO(), p.CS())
		}
	}
	if timerInt == 0 {
		for {
			runTx(c, *logger, cli)
			time.Sleep(time.Duration(100) * time.Millisecond)
		}
	} else {
		meterTicker := time.NewTicker(time.Minute * time.Duration(timerInt))
		go func() {
			for t := range meterTicker.C {
				fmt.Println(t)
				runTx(c, *logger, cli)
			}
		}()
		for {
			time.Sleep(time.Duration(100) * time.Hour)
		}
	}

}
