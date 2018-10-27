package hosts

import (
	"context"
	"sync"
	"time"

	"github.com/choria-io/provisioning-agent/host"
)

func provisioner(ctx context.Context, wg *sync.WaitGroup, i int) {
	defer wg.Done()

	log.Debugf("Provisioner worker %d starting", i)

	for {
		select {
		case host := <-work:
			log.Infof("Provisioning %s", host.Identity)

			delay, err := provisionTarget(ctx, host)
			if err != nil {
				provErrCtr.WithLabelValues(conf.Site).Inc()
				log.Errorf("Could not provision %s: %s", host.Identity, err)
			}

			// delay removing the node to avoid a race between discovery and node restarting splay
			// but during upgrades we'll want to remove the node immediately so its restart again
			// causes a provision to be done
			go func() {
				<-time.NewTimer(delay).C
				done <- host
			}()

		case <-ctx.Done():
			log.Infof("Worker %d exiting on context", i)
			return
		}
	}
}

func provisionTarget(ctx context.Context, target *host.Host) (cleanDelay time.Duration, err error) {
	busyWorkerGauge.WithLabelValues(conf.Site).Inc()
	defer busyWorkerGauge.WithLabelValues(conf.Site).Dec()

	delay, err := target.Provision(ctx, fw)
	if err != nil {
		return delay, err
	}

	provisionedCtr.WithLabelValues(conf.Site).Inc()

	return delay, nil
}

func finisher(ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()

	for {
		select {
		case host := <-done:
			log.Debugf("Removing %s from the provision list", host.Identity)
			remove(host)
		case <-ctx.Done():
			log.Info("Finisher exiting on context")
			return
		}
	}
}
