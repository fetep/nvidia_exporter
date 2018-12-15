package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type nvidiaStat struct {
	name   string
	metric prometheus.GaugeVec
}

var (
	interval = flag.Duration("interval", 5*time.Second, "how often to request stats from nvidia-smi")
	port     = flag.Int("port", 9523, "http port to expose metrics on")
	stats    = []nvidiaStat{
		nvidiaStat{
			name: "memory.used",
			metric: *prometheus.NewGaugeVec(prometheus.GaugeOpts{
				Name: "nvidia_memory_used_megabytes",
				Help: "Total memory allocated by active contexts",
			}, []string{"gpu"}),
		},
		nvidiaStat{
			name: "memory.total",
			metric: *prometheus.NewGaugeVec(prometheus.GaugeOpts{
				Name: "nvidia_memory_total_megabytes",
				Help: "Total installed GPU memory",
			}, []string{"gpu"}),
		},
		nvidiaStat{
			name: "utilization.gpu",
			metric: *prometheus.NewGaugeVec(prometheus.GaugeOpts{
				Name: "nvidia_gpu_utilization_percent",
				Help: "Percent of time over the past sample period during which one or more kernels was executing on the GPU",
			}, []string{"gpu"}),
		},
		nvidiaStat{
			name: "utilization.memory",
			metric: *prometheus.NewGaugeVec(prometheus.GaugeOpts{
				Name: "nvidia_memory_utilization_percent",
				Help: "Percent of time over the past sample period during which global (device) memory was being read or written",
			}, []string{"gpu"}),
		},
		nvidiaStat{
			name: "temperature.gpu",
			metric: *prometheus.NewGaugeVec(prometheus.GaugeOpts{
				Name: "nvidia_temperature_celsius",
				Help: "Core GPU temperature",
			}, []string{"gpu"}),
		},
		nvidiaStat{
			name: "power.draw",
			metric: *prometheus.NewGaugeVec(prometheus.GaugeOpts{
				Name: "nvidia_power_draw_watts",
				Help: "The last measured power draw for the entire board",
			}, []string{"gpu"}),
		},
	}
)

func scrapeSmi() {
	lastUpdated := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "nvidia_last_updated_time",
		Help: "Last time that we read output from nvidia-smi",
	})
	prometheus.MustRegister(lastUpdated)

	queryValues := []string{"index"}
	for _, stat := range stats {
		prometheus.MustRegister(stat.metric)
		queryValues = append(queryValues, stat.name)
	}
	queryOpt := fmt.Sprintf("--query-gpu=%s", strings.Join(queryValues, ","))

	seconds := fmt.Sprintf("%.0f", interval.Seconds())
	if seconds == "0" {
		log.Fatalf("interval must be at least 1 second")
	}

	cmd := exec.Command("nvidia-smi", "-l", seconds, "--format=csv,noheader,nounits", queryOpt)
	cmdStdout, _ := cmd.StdoutPipe()
	cmdStdoutReader := bufio.NewReader(cmdStdout)

	log.Printf("Running %s", strings.Join(cmd.Args, " "))
	cmd.Start()

	for {
		line, err := cmdStdoutReader.ReadBytes('\n')
		if err != nil {
			log.Fatalf("error reading nvidia-smi output: %s", err)
		}
		lastUpdated.Set(float64(time.Now().Unix()))

		line = line[:len(line)-1] // drop the \n

		// This isn't the best CSV parsing, but none of the data
		// should ever contain a "," or need anything fancier.
		data := strings.Split(string(line), ", ")

		// We should have an output field for each stat plus the index
		if len(data) != len(stats) + 1 {
			log.Fatalf("invalid nvidia-smi output: %s", line)
		}

		gpu := data[0]
		for i, stat := range stats {
			value, err := strconv.ParseFloat(data[i+1], 64)
			if err != nil {
				log.Fatalf("error converting %s value (%s) to float: %s", stat.name, data[i+1], err)
			}
			stat.metric.With(prometheus.Labels{"gpu": gpu}).Set(value)
		}
	}

	// If we end up here, something went horribly wrong with nvidia-smi
	cmd.Wait()
	os.Exit(1)
}

func main() {
	flag.Parse()

	go scrapeSmi()

	addr := fmt.Sprintf(":%d", *port)
	http.Handle("/metrics", promhttp.Handler())
	log.Printf("Starting HTTP listener on %s", addr)
	log.Fatal(http.ListenAndServe(addr, nil))
}
