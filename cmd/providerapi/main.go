package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/chenming/providerapi/internal/app"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd := os.Args[1]
	os.Args = append([]string{os.Args[0]}, os.Args[2:]...)
	var err error
	switch cmd {
	case "serve":
		err = cmdServe()
	case "models":
		err = cmdGet("/admin/models")
	case "plugins":
		err = cmdGet("/admin/plugins")
	case "requests":
		err = cmdGet("/admin/requests")
	case "trace":
		err = cmdTrace()
	case "help", "-h", "--help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", cmd)
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `providerapi

Usage:
  providerapi serve -c config.yaml
  providerapi models
  providerapi plugins
  providerapi requests
  providerapi trace <request-id>
`)
}

func cmdServe() error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	cfgPath := fs.String("c", "config.yaml", "config file")
	if err := fs.Parse(os.Args[1:]); err != nil {
		return err
	}
	return app.Serve(*cfgPath)
}

func adminFlags(fs *flag.FlagSet) (*string, *string) {
	admin := fs.String("admin", "http://127.0.0.1:8318", "admin base URL")
	token := fs.String("admin-token", "", "admin token")
	return admin, token
}

func cmdGet(path string) error {
	fs := flag.NewFlagSet("get", flag.ExitOnError)
	admin, token := adminFlags(fs)
	if err := fs.Parse(os.Args[1:]); err != nil {
		return err
	}
	body, err := adminGET(*admin, *token, path)
	if err != nil {
		return err
	}
	fmt.Println(string(body))
	return nil
}

func cmdTrace() error {
	fs := flag.NewFlagSet("trace", flag.ExitOnError)
	admin, token := adminFlags(fs)
	if err := fs.Parse(os.Args[1:]); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return fmt.Errorf("usage: providerapi trace <request-id>")
	}
	id := fs.Arg(0)
	body, err := adminGET(*admin, *token, "/admin/requests/"+id)
	if err != nil {
		return err
	}
	var data map[string]any
	if err := json.Unmarshal(body, &data); err != nil {
		fmt.Println(string(body))
		return nil
	}
	printTrace(id, data)
	return nil
}

func printTrace(id string, data map[string]any) {
	fmt.Printf("Request: %s\n\n", id)
	client, _ := data["client"].(map[string]any)
	routing, _ := data["routing"].(map[string]any)
	usage, _ := data["usage"].(map[string]any)
	fmt.Println("Client")
	fmt.Printf("  model: %v\n", client["model"])
	fmt.Println()
	fmt.Println("Routing")
	fmt.Printf("  provider: %v\n", routing["provider"])
	fmt.Printf("  upstream: %v\n", routing["upstream_model"])
	if n, ok := data["normalized"].(map[string]any); ok {
		fmt.Printf("  reasoning: %v\n", n["reasoning"])
	}
	fmt.Println()
	fmt.Println("Plugin")
	fmt.Printf("  %v@%v\n", routing["plugin"], routing["plugin_version"])
	fmt.Println()
	fmt.Println("Timing")
	fmt.Printf("  ttft: %vms\n", usage["ttft_ms"])
	dur, _ := usage["duration_ms"].(float64)
	fmt.Printf("  total: %.2fs\n", dur/1000)
	fmt.Println()
	fmt.Println("Usage")
	fmt.Printf("  input: %v\n", usage["input_tokens"])
	fmt.Printf("  output: %v\n", usage["output_tokens"])
	fmt.Println()
	fmt.Println("Result")
	if errv, ok := data["error"].(map[string]any); ok && errv != nil {
		fmt.Printf("  error: %v\n", errv["message"])
	} else {
		fmt.Printf("  status: ok\n")
	}
}

func adminGET(base, token, path string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+path, nil)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("X-Admin-Token", token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("admin %s: %s", resp.Status, b)
	}
	return b, nil
}
