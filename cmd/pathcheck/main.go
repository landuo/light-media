package main

import (
	"encoding/json"
	"flag"
	"os"

	"light-media/internal/pathprobe"
)

func main() {
	source := flag.String("source-dir", "", "existing, explicitly authorized source test directory")
	target := flag.String("target-dir", "", "existing, explicitly authorized target test directory")
	flag.Parse()
	if *source == "" || *target == "" || flag.NArg() != 0 {
		json.NewEncoder(os.Stdout).Encode(map[string]string{"status": "failed", "reason": "source_and_target_directory_flags_required"})
		os.Exit(1)
	}
	r, err := pathprobe.Run(*source, *target)
	if encodeErr := json.NewEncoder(os.Stdout).Encode(r); encodeErr != nil {
		os.Exit(1)
	}
	if err != nil {
		os.Exit(2)
	}
}
