package host

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/choria-io/go-protocol/protocol"
	"github.com/choria-io/mcorpc-agent-provider/mcorpc"
	rpc "github.com/choria-io/mcorpc-agent-provider/mcorpc/client"
	provision "github.com/choria-io/provisioning-agent/agent"
	"github.com/prometheus/client_golang/prometheus"
)

func (h *Host) rpcDo(ctx context.Context, agent string, action string, input interface{}, cb rpc.Handler) (*rpc.Stats, error) {
	name := fmt.Sprintf("%s#%s", agent, action)

	obs := prometheus.NewTimer(rpcDuration.WithLabelValues(h.cfg.Site, name))
	defer obs.ObserveDuration()

	if h.cfg.Paused() {
		return nil, fmt.Errorf("Provisioning is paused, cannot perform %s", name)
	}

	ddl, ok := provision.DDL[agent]
	if !ok {
		return nil, fmt.Errorf("could not find DDL for agent %s in the provision.DDL structure", agent)
	}
	prov, err := rpc.New(h.fw, agent, rpc.DDL(ddl))
	if err != nil {
		rpcErrCtr.WithLabelValues(h.cfg.Site, name).Inc()
		return nil, fmt.Errorf("could not create %s client: %s", agent, err)
	}

	handler := func(pr protocol.Reply, reply *rpc.RPCReply) {
		h.replylock.Lock()
		defer h.replylock.Unlock()

		if reply.Statuscode != mcorpc.OK {
			rpcErrCtr.WithLabelValues(h.cfg.Site, name).Inc()
			h.log.Errorf("Failed reply from %s: %s", pr.SenderID(), reply.Statusmsg)
			return
		}

		if pr.SenderID() == h.Identity {
			cb(pr, reply)
		}
	}

	result, err := prov.Do(ctx, action, input, rpc.Targets([]string{h.Identity}), rpc.Collective("provisioning"), rpc.ReplyHandler(handler), rpc.Workers(1))
	if err != nil {
		rpcErrCtr.WithLabelValues(h.cfg.Site, name).Inc()
		return nil, fmt.Errorf("could not perform %s#%s: %s", agent, action, err)
	}

	if result.Stats().ResponsesCount() != 1 {
		rpcErrCtr.WithLabelValues(h.cfg.Site, name).Inc()
		return nil, fmt.Errorf("cound not perform %s#%s: received %d responses while expecting a response from %s", agent, action, result.Stats().ResponsesCount(), h.Identity)
	}

	return result.Stats(), nil

}

func (h *Host) upgrade(ctx context.Context) (upgraded bool, err error) {
	if h.desiredVersion == "" {
		return false, nil
	}

	current, err := h.Version()
	if err != nil {
		return false, fmt.Errorf("could not determine current version: %s", err)
	}

	if current == h.desiredVersion {
		return false, nil
	}

	h.log.Infof("Upgrading %s from %s to %s", h, current, h.desiredVersion)

	ureq := &provision.ReleaseUpdateRequest{
		Repository: h.cfg.UpdateRepository,
		Token:      h.cfg.Token,
		Version:    h.desiredVersion,
	}

	_, err = h.rpcDo(ctx, "choria_provision", "release_update", ureq, func(pr protocol.Reply, reply *rpc.RPCReply) {
		if reply.Statuscode != mcorpc.OK {
			h.log.Errorf("Upgrading %s failed: %s", h, reply.Statusmsg)
			err = fmt.Errorf(reply.Statusmsg)
			return
		}

		r := &provision.Reply{}
		err := json.Unmarshal(reply.Data, r)
		if err != nil {
			h.log.Errorf("Could not parse reply from %s: %s", pr.SenderID(), err)
			return
		}

		h.log.Infof("Upgrade response: %s", r.Message)
		upgraded = true
	})

	return upgraded, err
}

func (h *Host) restart(ctx context.Context) error {
	h.log.Info("Restarting node")

	creq := &provision.RestartRequest{
		Token: h.token,
		Splay: 1,
	}

	_, err := h.rpcDo(ctx, "choria_provision", "restart", creq, func(pr protocol.Reply, reply *rpc.RPCReply) {
		r := &provision.Reply{}
		err := json.Unmarshal(reply.Data, r)
		if err != nil {
			h.log.Errorf("Could not parse reply from %s: %s", pr.SenderID(), err)
			return
		}

		h.log.Infof("Restart response: %s", r.Message)
	})

	return err
}

func (h *Host) configure(ctx context.Context) error {
	if len(h.config) == 0 {
		return fmt.Errorf("empty configuration")
	}

	h.log.Info("Configuring node")

	cj, err := json.Marshal(h.config)
	if err != nil {
		return fmt.Errorf("could not encode configuration: %s", err)
	}

	creq := &provision.ConfigureRequest{
		Token:         h.token,
		CA:            h.ca,
		Certificate:   h.cert,
		Configuration: string(cj),
	}

	if h.CSR != nil {
		creq.SSLDir = h.CSR.SSLDir
	}

	_, err = h.rpcDo(ctx, "choria_provision", "configure", creq, func(pr protocol.Reply, reply *rpc.RPCReply) {
		r := &provision.Reply{}
		err := json.Unmarshal(reply.Data, r)
		if err != nil {
			h.log.Errorf("Could not parse reply from %s: %s", pr.SenderID(), err)
			return
		}

		h.log.Infof("Configuration response: %s", r.Message)
	})

	return err
}

func (h *Host) fetchInventory(ctx context.Context) error {
	if len(h.Metadata) > 0 {
		h.log.Infof("Already have metadata for %s, not retrieving again", h.Identity)
		return nil
	}

	h.log.Info("Fetching Inventory")

	_, err := h.rpcDo(ctx, "rpcutil", "inventory", struct{}{}, func(pr protocol.Reply, reply *rpc.RPCReply) {
		h.Metadata = string(reply.Data)
	})

	return err
}

func (h *Host) fetchCSR(ctx context.Context) error {
	h.log.Info("Fetching CSR")

	csreq := &provision.CSRRequest{
		Token: h.token,
		CN:    h.Identity,
	}

	_, err := h.rpcDo(ctx, "choria_provision", "gencsr", csreq, func(pr protocol.Reply, reply *rpc.RPCReply) {
		h.CSR = &provision.CSRReply{}
		err := json.Unmarshal(reply.Data, h.CSR)
		if err != nil {
			h.log.Errorf("Could not parse reply from %s: %s", pr.SenderID(), err)
			return
		}
	})

	return err
}
