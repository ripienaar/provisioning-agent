package host

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/choria-io/go-choria/choria"
	provision "github.com/choria-io/provisioning-agent/agent"
	"github.com/choria-io/provisioning-agent/config"
	"github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
)

type Host struct {
	Identity       string              `json:"identity"`
	CSR            *provision.CSRReply `json:"csr"`
	Metadata       string              `json:"inventory"`
	config         map[string]string
	provisioned    bool
	ca             string
	cert           string
	desiredVersion string

	cfg       *config.Config
	token     string
	fw        *choria.Framework
	log       *logrus.Entry
	mu        *sync.Mutex
	replylock *sync.Mutex
}

func NewHost(identity string, conf *config.Config) *Host {
	return &Host{
		Identity:    identity,
		provisioned: false,
		mu:          &sync.Mutex{},
		replylock:   &sync.Mutex{},
		token:       conf.Token,
		cfg:         conf,
	}
}

// Provision provisions an individual node
func (h *Host) Provision(ctx context.Context, fw *choria.Framework) (cleanDelay time.Duration, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.provisioned {
		return time.Duration(0), nil
	}

	h.fw = fw
	h.log = fw.Logger(h.Identity)

	err = h.fetchInventory(ctx)
	if err != nil {
		return time.Duration(0), fmt.Errorf("could not provision %s: %s", h.Identity, err)
	}

	if h.cfg.Features.PKI {
		err = h.fetchCSR(ctx)
		if err != nil {
			return time.Duration(0), fmt.Errorf("could not provision %s: %s", h.Identity, err)
		}
	}

	config, err := h.getConfig(ctx)
	if err != nil {
		helperErrCtr.WithLabelValues(h.cfg.Site).Inc()
		return time.Duration(0), err
	}

	if config.Defer {
		return time.Duration(0), fmt.Errorf("configuration defered: %s", config.Msg)
	}

	h.config = config.Configuration
	h.ca = config.CA
	h.cert = config.Certificate
	h.desiredVersion = config.Version

	upgraded, err := h.upgrade(ctx)
	if err != nil {
		return time.Duration(0), fmt.Errorf("upgrading failed: %s", err)
	}

	if upgraded {
		h.log.Infof("%s was upgraded, exiting provisioning flow early")
		h.provisioned = true
		return time.Duration(0), nil
	}

	err = h.configure(ctx)
	if err != nil {
		return time.Duration(0), fmt.Errorf("configuration failed: %s", err)
	}

	err = h.restart(ctx)
	if err != nil {
		return time.Duration(0), fmt.Errorf("restart failed: %s", err)
	}

	h.provisioned = true

	return time.Duration(20 * time.Second), nil
}

func (h *Host) String() string {
	return h.Identity
}

// Version gathers the version of the remote node from its inventory
func (h *Host) Version() (string, error) {
	v := gjson.Get(h.Metadata, "version")

	if !v.Exists() {
		return "", fmt.Errorf("inventory does not contain version")
	}

	return v.String(), nil
}
