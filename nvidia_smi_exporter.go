package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"time"
)

var (
	addr       = ":9100"
	textPath   = ""
	textUpdate = 5000 * time.Millisecond
)

func init() {
	flag.StringVar(&addr, "addr", addr, "addr to listen on for HTTP")
	flag.StringVar(&textPath, "textPath", textPath, "path to write text profiling to.  Set this and we won't have an http handler")
	flag.DurationVar(&textUpdate, "textUpdate", textUpdate, "time between updating analytics")
}

// name, index, temperature.gpu, utilization.gpu,
// utilization.memory, memory.total, memory.free, memory.used

func metrics(response http.ResponseWriter, request *http.Request) {
	if err := writeMetrics(response); err != nil {
		response.WriteHeader(500)
		fmt.Fprintf(response, "%v", err)
		log.Printf("%v", err)
	}
}

func writeMetrics(w io.Writer) error {
	out, err := exec.Command(
		"nvidia-smi",
		"--query-gpu=name,index,temperature.gpu,utilization.gpu,utilization.memory,memory.total,memory.free,memory.used",
		"--format=csv,noheader,nounits").Output()

	if err != nil {
		return err
	}

	csvReader := csv.NewReader(bytes.NewReader(out))
	csvReader.TrimLeadingSpace = true
	records, err := csvReader.ReadAll()

	if err != nil {
		return err
	}

	metricList := []string{
		"temperature.gpu", "utilization.gpu",
		"utilization.memory", "memory.total", "memory.free", "memory.used"}

	result := ""
	for _, row := range records {
		name := fmt.Sprintf("%s[%s]", row[0], row[1])
		for idx, value := range row[2:] {
			result = fmt.Sprintf(
				"%s%s{gpu=\"%s\"} %s\n", result,
				metricList[idx], name, value)
		}
	}

	fmt.Fprintf(w, strings.Replace(result, ".", "_", -1))
	return nil
}

func main() {
	flag.Parse()

	sig := make(chan os.Signal)

	signal.Notify(sig, os.Interrupt)

	if textPath == "" {
		mux := http.NewServeMux()

		mux.HandleFunc("/metrics/", metrics)

		srv := &http.Server{
			Addr:    addr,
			Handler: mux,
		}

		go func() {
			log.Printf("starting serve on %s", addr)
			if err := srv.ListenAndServe(); err != nil {
				log.Fatal(err)
			}
		}()

		<-sig

		srv.Shutdown(context.Background())

		return
	}

	log.Printf("outputing to: %s", textPath)

outer_loop:
	for {
		select {
		case <-sig:
			break outer_loop
		default:
		}

		var file io.WriteCloser

		if textPath == "-" {
			file = os.Stdout
		} else {
			var err error
			file, err = os.OpenFile(textPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0660)
			if err != nil {
				log.Fatal(err)
			}
		}

		writeMetrics(file)

		if textPath != "-" {
			file.Close()
		}

		timer := time.NewTimer(textUpdate)
		select {
		case <-sig:
			break outer_loop
		case <-timer.C:
		}
	}
}
