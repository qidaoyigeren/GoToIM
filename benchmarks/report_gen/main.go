// report.go - Generates an HTML report from benchmark JSON results.
//
// Usage:
//
//	go run benchmarks/report.go -conn=conn_results.json -push=push_results.json -output=report.html
package main

import (
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"os"
)

//go:embed report_template.html
var templateFS embed.FS

var (
	connFile string
	pushFile string
	outFile  string
)

func init() {
	flag.StringVar(&connFile, "conn", "", "connection benchmark JSON file")
	flag.StringVar(&pushFile, "push", "", "push benchmark JSON file")
	flag.StringVar(&outFile, "output", "report.html", "output HTML file")
}

func main() {
	flag.Parse()

	data := map[string]interface{}{}

	if connFile != "" {
		raw, err := os.ReadFile(connFile)
		if err != nil {
			fmt.Printf("Warning: cannot read %s: %v\n", connFile, err)
		} else {
			var connReport map[string]interface{}
			json.Unmarshal(raw, &connReport)
			data["conn"] = connReport
			data["connJSON"] = string(raw)
		}
	}

	if pushFile != "" {
		raw, err := os.ReadFile(pushFile)
		if err != nil {
			fmt.Printf("Warning: cannot read %s: %v\n", pushFile, err)
		} else {
			var pushReport map[string]interface{}
			json.Unmarshal(raw, &pushReport)
			data["push"] = pushReport
			data["pushJSON"] = string(raw)
		}
	}

	tmplContent, err := templateFS.ReadFile("report_template.html")
	if err != nil {
		fmt.Printf("Error reading template: %v\n", err)
		os.Exit(1)
	}

	tmpl, err := template.New("report").Parse(string(tmplContent))
	if err != nil {
		fmt.Printf("Error parsing template: %v\n", err)
		os.Exit(1)
	}

	f, err := os.Create(outFile)
	if err != nil {
		fmt.Printf("Error creating %s: %v\n", outFile, err)
		os.Exit(1)
	}
	defer f.Close()

	if err := tmpl.Execute(f, data); err != nil {
		fmt.Printf("Error rendering template: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Report generated: %s\n", outFile)
}
