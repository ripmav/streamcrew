// SPDX-License-Identifier: Apache-2.0

// Command helper is the program the tests of package host start. It is a
// program of its own, not the test binary, so that it can never run the
// tests again and start itself without end, whatever the environment or
// the arguments are. The first argument chooses what it does.
package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"time"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		return 2
	}
	out := os.Stdout
	switch args[0] {
	case "echo": // each argument on a line
		for _, a := range args[1:] {
			fmt.Fprintln(out, a)
		}
	case "pwd": // the working directory
		wd, _ := os.Getwd()
		fmt.Fprintln(out, wd)
	case "env": // the environment, sorted
		env := os.Environ()
		slices.Sort(env)
		for _, kv := range env {
			fmt.Fprintln(out, kv)
		}
	case "exit": // a line on each output, then the exit code of the second argument
		fmt.Fprintln(out, "bye")
		fmt.Fprintln(os.Stderr, "on stderr")
		code, _ := strconv.Atoi(arg(args, 1))
		return code
	case "flood": // as many bytes as the second argument says
		n, _ := strconv.Atoi(arg(args, 1))
		_, _ = out.Write(bytes.Repeat([]byte("x"), min(max(n, 0), 8<<20)))
		_, _ = os.Stderr.WriteString("e")
	case "sleep": // a line, then sleep for the duration of the second argument, at most a minute
		fmt.Fprintln(out, "started")
		d, _ := time.ParseDuration(arg(args, 1))
		time.Sleep(min(d, time.Minute))
	case "touch": // create the marker of the number in the second argument
		id, _ := strconv.Atoi(arg(args, 1))
		_ = os.WriteFile(markerName(id), []byte("touched"), 0o600)
	case "spawn": // start one program that touches the marker later, then sleep
		id, _ := strconv.Atoi(arg(args, 1))
		self, _ := os.Executable()
		cmd := exec.CommandContext(context.Background(), self, "later")
		cmd.Env = append(os.Environ(), "HOST_TEST_MARKER="+strconv.Itoa(id))
		_ = cmd.Start()
		fmt.Fprintln(out, "spawned")
		time.Sleep(time.Minute)
	case "later": // touch the marker of HOST_TEST_MARKER after two seconds
		id, _ := strconv.Atoi(os.Getenv("HOST_TEST_MARKER"))
		time.Sleep(2 * time.Second)
		_ = os.WriteFile(markerName(id), []byte("late"), 0o600)
	default:
		return 2
	}
	return 0
}

// arg returns args[i]; empty if there is none.
func arg(args []string, i int) string {
	if i < len(args) {
		return args[i]
	}
	return ""
}

// markerName returns the name of the marker file number id. The program
// writes it into its working directory, which is its own directory
// (actions.md B117).
func markerName(id int) string {
	return "streamcrew-host-marker-" + strconv.Itoa(id)
}
