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
	BasicVerbose bool   `json:"BasicVerbose"`
	BasicLogger  bool   `json:"BasicLogger"`
	BasicTimer   int    `json:"BasicTimer"`
	MqttAddress  string `json:"MqttAddress"`
	MqttTopic    string `json:"MqttTopic"`
}

type Mqttdata struct {
	V1V8       float64 `json:"1V8"`
	V3V3       float64 `json:"3V3"`
	V5V        float64 `json:"5V"`
	V48V       float64 `json:"48V"`
	IbatteryI  float64 `json:"BatteryI"`
	VbatteryV  float64 `json:"BatteryV"`
	IsolarI    float64 `json:"SolarI"`
	VsolarV    float64 `json:"SolarV"`
	Time       string  `json:"Time"`
	DeviceName string  `json:"DeviceName"`
}

type ErrorMqttdata struct {
	Name       string `json:"Name"`
	Value      string `json:"Value"`
	Time       string `json:"Time"`
	DeviceName string `json:"DeviceName"`
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

	mqttData := &Mqttdata{}
	mqttData.VsolarV = 0
	var valuesBool map[string]bool
	valuesBool = make(map[string]bool)

	t := time.Now()
	mqttData.Time = t.String()
	host, _ := os.Hostname()
	mqttData.DeviceName = host

	for command := byte(0x80); command >= 0x80 && command <= 0xF0; command += 0x10 {
		multip, _ := commands[command]
		tempfloat, err := getMCP3008Value(s, command)

		if err != nil {
			fmt.Fprintf(os.Stderr, "spi-io: %s.\n", err)
			os.Exit(1)
		}
		switch command {
		case 0x80:
			mqttData.V1V8 = tempfloat * multip
			if 1.7 <= mqttData.V1V8 && mqttData.V1V8 <= 1.9 {
				valuesBool["1V8"] = true
			} else {
				valuesBool["1V8"] = false
			}
		case 0x90:
			mqttData.V3V3 = tempfloat * multip
			if 3.1 <= mqttData.V3V3 && mqttData.V3V3 <= 3.5 {
				valuesBool["3V3"] = true
			} else {
				valuesBool["3V3"] = false
			}
		case 0xA0:
			mqttData.VbatteryV = tempfloat * multip
			if 3.5 <= mqttData.VbatteryV && mqttData.VbatteryV <= 4.3 {
				valuesBool["batteryV"] = true
			} else {
				valuesBool["batteryV"] = false
			}
		case 0xB0:
			mqttData.V5V = tempfloat * multip
			if 4.7 <= mqttData.V5V && mqttData.V5V <= 5.3 {
				valuesBool["5V"] = true
			} else {
				valuesBool["5V"] = false
			}

		case 0xC0:
			mqttData.V48V = tempfloat*multip + 0.7
			if 45 <= mqttData.V48V && mqttData.V48V <= 50 {
				valuesBool["48V"] = true
			} else {
				valuesBool["48V"] = false
			}
		case 0xD0:
			mqttData.VsolarV = tempfloat*multip + 0.8
			if 10 <= mqttData.VsolarV && mqttData.VsolarV <= 28 {
				valuesBool["solarV"] = true
			} else {
				valuesBool["solarV"] = false
			}
		case 0xE0:
			mqttData.IbatteryI = (tempfloat - 1.8) * multip
		case 0xF0:
			if mqttData.VsolarV != 0 {
				mqttData.VsolarV = (tempfloat - 1.8) * multip * 4.13 / mqttData.VsolarV
			}
		default:
			fmt.Fprintf(os.Stderr, "case_error\n")
			os.Exit(1)
		}
	}
	fmt.Println(valuesBool, beforeValuesBool)
	for name, bol := range valuesBool {

		if beforeValuesBool[name] != bol {
			str := "off"
			if !beforeValuesBool[name] && bol {
				str = "on"
			}

			errorMqttData := &ErrorMqttdata{}
			errorMqttData.Time = t.String()
			errorMqttData.DeviceName = host
			errorMqttData.Name = name
			errorMqttData.Value = str

			errorJsonString, _ := json.Marshal(errorMqttData)

			err := cli.Publish(&client.PublishOptions{
				QoS:       mqtt.QoS0,
				TopicName: []byte("log"),
				Message:   []byte(errorJsonString),
			})
			if err != nil {
				panic(err)
			}

		}

	}
	beforeValuesBool = valuesBool
	if logger {
		fmt.Println(mqttData)
	}

	jsonString, _ := json.Marshal(mqttData)

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
		meterTicker := time.NewTicker(time.Second * time.Duration(timerInt))
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
