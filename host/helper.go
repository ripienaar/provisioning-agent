package host

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"time"

	"github.com/choria-io/provisioning-agent/config"
	"github.com/prometheus/client_golang/prometheus"
)

type ConfigResponse struct {
	Defer         bool              `json:"defer"`
	Msg           string            `json:"msg"`
	Certificate   string            `json:"certificate"`
	CA            string            `json:"ca"`
	Version       string            `json:"version"`
	Configuration map[string]string `json:"configuration"`
}

func (h *Host) getConfig(ctx context.Context) (*ConfigResponse, error) {
	r := &ConfigResponse{}

	input, err := json.Marshal(h)
	if err != nil {
		return nil, fmt.Errorf("could not JSON encode host: %s", err)
	}

	err = runDecodedHelper(ctx, []string{}, string(input), r, h.cfg)
	if err != nil {
		return nil, fmt.Errorf("could not invoke configure helper: %s", err)
	}

	return r, nil
}

func runDecodedHelper(ctx context.Context, args []string, input string, output interface{}, cfg *config.Config) error {
	o, err := runHelper(ctx, args, input, cfg)
	if err != nil {
		return err
	}

	err = json.Unmarshal(o, output)
	if err != nil {
		return fmt.Errorf("cannot decode output from %s: %s", cfg.Helper, err)
	}

	return nil
}

func runHelper(ctx context.Context, args []string, input string, cfg *config.Config) ([]byte, error) {
	obs := prometheus.NewTimer(helperDuration.WithLabelValues(cfg.Site))
	defer obs.ObserveDuration()

	if cfg.Paused() {
		return nil, fmt.Errorf("Provisioning is paused, cannot perform %s", cfg.Helper)
	}

	tctx, cancel := context.WithTimeout(ctx, time.Duration(10*time.Second))
	defer cancel()

	execution := exec.CommandContext(tctx, cfg.Helper, args...)

	stdin, err := execution.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("cannot create stdin for %s: %s", cfg.Helper, err)
	}

	go func() {
		defer stdin.Close()
		io.WriteString(stdin, input)
	}()

	stdout, err := execution.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("cannot open STDOUT for %s: %s", cfg.Helper, err)
	}
	defer stdout.Close()

	err = execution.Start()
	if err != nil {
		return nil, fmt.Errorf("cannot start %s: %s", cfg.Helper, err)
	}

	buf := new(bytes.Buffer)

	n, err := buf.ReadFrom(stdout)
	if err != nil {
		return nil, fmt.Errorf("cannot read %s output: %s", cfg.Helper, err)
	}
	if n == 0 {
		return nil, fmt.Errorf("cannot read %s output: zero bytes received", cfg.Helper)
	}

	err = execution.Wait()
	if err != nil {
		return nil, fmt.Errorf("could not wait for %s: %s", cfg.Helper, err)
	}

	if !execution.ProcessState.Success() {
		return nil, fmt.Errorf("could not run helper %s: exited with non 0 exitcode", cfg.Helper)
	}

	return buf.Bytes(), nil
}
