package acceptance

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type managedPolicyOwnerMetrics struct {
	fresh         float64
	stale         float64
	unconfigured  float64
	syncFailures  float64
	auditFailures float64
}

type managedPolicyMetrics struct {
	owners        []managedPolicyOwnerMetrics
	fresh         float64
	stale         float64
	unconfigured  float64
	syncFailures  float64
	auditFailures float64
}

func managedPolicyAuditRecovery(ctx context.Context, w wire) (resultErr error) {
	expectedOwners := 1
	if w.env.RunInfo.MultiReplica {
		expectedOwners = 2
	}
	baseline, err := managedPolicyMetricSnapshot(ctx, w)
	if err != nil {
		return err
	}
	if !everyManagedPolicyOwner(baseline, baseline, expectedOwners, func(current, _ managedPolicyOwnerMetrics) bool {
		return current.fresh == 1 && current.stale == 0 && current.unconfigured == 0
	}) {
		return fmt.Errorf("%s managed table policy was not fresh before fault injection", w.kind)
	}
	if err := w.env.StopMetadataDatabase(ctx); err != nil {
		return err
	}
	databaseStopped := true
	defer func() {
		if !databaseStopped {
			return
		}
		restoreCtx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if err := w.env.StartMetadataDatabase(restoreCtx); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("restore reference metadata database: %w", err))
		}
	}()

	if w.kind == rest {
		if err := triggerRESTAuditFailures(ctx, w); err != nil {
			return fmt.Errorf("trigger REST audit persistence failure on each owner: %w", err)
		}
	}
	outage, err := waitManagedPolicyMetrics(ctx, w, 35*time.Second, func(current managedPolicyMetrics) bool {
		return everyManagedPolicyOwner(current, baseline, expectedOwners, func(now, before managedPolicyOwnerMetrics) bool {
			return now.stale == 1 && now.fresh == 0 && now.unconfigured == 0 &&
				now.syncFailures > before.syncFailures &&
				now.auditFailures > before.auditFailures
		})
	}, "stale policy and failed audit persistence signals on every owner")
	if err != nil {
		return err
	}
	if outage.fresh != 0 || outage.unconfigured != 0 {
		return fmt.Errorf("%s managed table policy did not transition to stale on every owner", w.kind)
	}

	restoreCtx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	err = w.env.StartMetadataDatabase(restoreCtx)
	cancel()
	if err != nil {
		return err
	}
	databaseStopped = false

	recovered, err := waitManagedPolicyMetrics(ctx, w, 45*time.Second, func(current managedPolicyMetrics) bool {
		return everyManagedPolicyOwner(current, baseline, expectedOwners, func(now, before managedPolicyOwnerMetrics) bool {
			return now.fresh == 1 && now.stale == 0 && now.unconfigured == 0 &&
				now.syncFailures > before.syncFailures
		})
	}, "managed policy resynchronization on every owner")
	if err != nil {
		return err
	}
	if !everyManagedPolicyOwner(recovered, baseline, expectedOwners, func(now, before managedPolicyOwnerMetrics) bool {
		return now.auditFailures > before.auditFailures
	}) {
		return fmt.Errorf("%s audit persistence failure counter did not increase on every owner during metadata outage", w.kind)
	}
	if err := auditEvidence(ctx, w); err != nil {
		return fmt.Errorf("%s audit event did not persist after database recovery: %w", w.kind, err)
	}
	after, err := managedPolicyMetricSnapshot(ctx, w)
	if err != nil {
		return err
	}
	if !everyManagedPolicyOwner(after, recovered, expectedOwners, func(now, before managedPolicyOwnerMetrics) bool {
		return now.fresh == 1 && now.stale == 0 && now.unconfigured == 0 &&
			now.syncFailures == before.syncFailures &&
			now.auditFailures == before.auditFailures
	}) {
		return fmt.Errorf("%s policy or audit persistence did not remain healthy after recovery", w.kind)
	}
	return nil
}

func triggerRESTAuditFailures(ctx context.Context, w wire) error {
	endpoints := []string{w.env.APIURL}
	if w.env.RunInfo.MultiReplica {
		endpoints = []string{w.env.OwnerURLs["rest-a"], w.env.OwnerURLs["rest-b"]}
	}
	for _, endpoint := range endpoints {
		response, err := w.request(ctx, http.MethodGet, endpoint+"/api/v1/database/connections/stats",
			w.env.Identities["operator"].RESTToken, "", nil)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(io.Discard, io.LimitReader(response.Body, maxResponse))
		response.Body.Close()
		if copyErr != nil {
			return fmt.Errorf("read REST connection-pool diagnostic response: %w", copyErr)
		}
		if response.StatusCode != http.StatusOK {
			return fmt.Errorf("REST connection-pool diagnostic returned HTTP %d", response.StatusCode)
		}
	}
	return nil
}

func managedPolicyMetricSnapshot(ctx context.Context, w wire) (managedPolicyMetrics, error) {
	bodies, err := acceptanceMetricBodies(ctx, w)
	if err != nil {
		return managedPolicyMetrics{}, err
	}
	transport := string(w.kind)
	snapshot := managedPolicyMetrics{owners: make([]managedPolicyOwnerMetrics, 0, len(bodies))}
	for _, body := range bodies {
		owner := managedPolicyOwnerMetrics{
			fresh:         sumMetricValue([]string{body}, "data_fabric_managed_table_policy_status", "state="+strconv.Quote("fresh")),
			stale:         sumMetricValue([]string{body}, "data_fabric_managed_table_policy_status", "state="+strconv.Quote("stale")),
			unconfigured:  sumMetricValue([]string{body}, "data_fabric_managed_table_policy_status", "state="+strconv.Quote("unconfigured")),
			syncFailures:  sumMetricValue([]string{body}, "data_fabric_managed_table_policy_sync_failures_total", ""),
			auditFailures: sumMetricValue([]string{body}, "data_fabric_audit_persist_failures_total", "transport="+strconv.Quote(transport)),
		}
		snapshot.owners = append(snapshot.owners, owner)
		snapshot.fresh += owner.fresh
		snapshot.stale += owner.stale
		snapshot.unconfigured += owner.unconfigured
		snapshot.syncFailures += owner.syncFailures
		snapshot.auditFailures += owner.auditFailures
	}
	return snapshot, nil
}

func everyManagedPolicyOwner(current, baseline managedPolicyMetrics, expected int, check func(managedPolicyOwnerMetrics, managedPolicyOwnerMetrics) bool) bool {
	if len(current.owners) != expected || len(baseline.owners) != expected {
		return false
	}
	for i, owner := range current.owners {
		if !check(owner, baseline.owners[i]) {
			return false
		}
	}
	return true
}

func sumMetricValue(bodies []string, name, label string) float64 {
	var total float64
	for _, body := range bodies {
		for _, line := range strings.Split(body, "\n") {
			if label == "" {
				if !strings.HasPrefix(line, name+" ") {
					continue
				}
			} else if !strings.HasPrefix(line, name+"{") || !strings.Contains(line, label) {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) != 2 {
				continue
			}
			value, err := strconv.ParseFloat(fields[1], 64)
			if err == nil {
				total += value
			}
		}
	}
	return total
}

func waitManagedPolicyMetrics(parent context.Context, w wire, timeout time.Duration, ready func(managedPolicyMetrics) bool, description string) (managedPolicyMetrics, error) {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	var last managedPolicyMetrics
	var lastErr error
	for {
		current, err := managedPolicyMetricSnapshot(ctx, w)
		if err == nil {
			last = current
			lastErr = nil
			if ready(current) {
				return current, nil
			}
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			if lastErr != nil {
				return last, fmt.Errorf("timed out waiting for %s: %w", description, lastErr)
			}
			return last, fmt.Errorf("timed out waiting for %s (fresh=%g stale=%g sync_failures=%g audit_failures=%g)",
				description, last.fresh, last.stale, last.syncFailures, last.auditFailures)
		case <-ticker.C:
		}
	}
}
