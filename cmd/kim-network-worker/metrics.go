package main

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/network/ovnruntime"
)

func newMetricsHandler(metrics *ovnruntime.Metrics, pool *pgxpool.Pool) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "text/plain; version=0.0.4")
		snapshot := metrics.Snapshot()
		for _, state := range []string{"STARTING", "ACTIVE", "DRAINING", "STOPPED"} {
			value := 0
			if snapshot.State == state {
				value = 1
			}
			fmt.Fprintf(response, "kim_ovn_worker_state{state=%q} %d\n", state, value)
		}
		fmt.Fprintf(response, "kim_ovn_worker_claim_runs_total %d\n", snapshot.ClaimRuns)
		fmt.Fprintf(response, "kim_ovn_worker_claim_errors_total %d\n", snapshot.ClaimErrors)
		fmt.Fprintf(response, "kim_ovn_worker_claims_total %d\n", snapshot.ClaimsTotal)
		fmt.Fprintf(response, "kim_ovn_worker_in_flight %d\n", snapshot.InFlight)
		fmt.Fprintf(response, "kim_ovn_worker_completed_total %d\n", snapshot.CompletedTotal)
		fmt.Fprintf(response, "kim_ovn_worker_item_errors_total %d\n", snapshot.ItemErrors)
		fmt.Fprintf(response, "kim_ovn_worker_fatal_errors_total %d\n", snapshot.FatalErrors)
		fmt.Fprintf(response, "kim_ovn_worker_renewals_total %d\n", snapshot.Renewals)
		fmt.Fprintf(response, "kim_ovn_worker_renewal_errors_total %d\n", snapshot.RenewalErrors)
		fmt.Fprintf(response, "kim_ovn_worker_claim_latency_seconds %g\n", snapshot.ClaimLatency.Seconds())
		fmt.Fprintf(response, "kim_ovn_worker_renewal_latency_seconds %g\n", snapshot.RenewalLatency.Seconds())
		fmt.Fprintf(response, "kim_ovn_worker_renewal_headroom_seconds %g\n", snapshot.RenewalHeadroom.Seconds())
		fmt.Fprintf(response, "kim_ovn_worker_oldest_in_flight_seconds %g\n", snapshot.OldestInFlightAge.Seconds())
		fmt.Fprintf(response, "kim_ovn_worker_drain_duration_seconds %g\n", snapshot.DrainDuration.Seconds())
		if pool == nil {
			return
		}
		stats := pool.Stat()
		fmt.Fprintf(response, "kim_ovn_worker_db_pool_acquired %d\n", stats.AcquiredConns())
		fmt.Fprintf(response, "kim_ovn_worker_db_pool_idle %d\n", stats.IdleConns())
		fmt.Fprintf(response, "kim_ovn_worker_db_pool_total %d\n", stats.TotalConns())
		fmt.Fprintf(response, "kim_ovn_worker_db_pool_empty_acquires_total %d\n", stats.EmptyAcquireCount())
		fmt.Fprintf(response, "kim_ovn_worker_db_pool_acquire_wait_seconds_total %g\n", stats.AcquireDuration().Seconds())

		queryContext, cancel := context.WithTimeout(request.Context(), time.Second)
		defer cancel()
		var pending, claimed, unknown, observed int64
		err := pool.QueryRow(queryContext, `SELECT
			count(*) FILTER (WHERE work_state='PENDING'),
			count(*) FILTER (WHERE work_state='CLAIMED'),
			count(*) FILTER (WHERE work_state='DISPATCH_UNKNOWN'),
			count(*) FILTER (WHERE work_state='OBSERVED')
			FROM kim.ovn_runtime_work_current`).Scan(&pending, &claimed, &unknown, &observed)
		if err != nil {
			fmt.Fprintln(response, "kim_ovn_worker_authority_scrape_error 1")
			return
		}
		fmt.Fprintln(response, "kim_ovn_worker_authority_scrape_error 0")
		fmt.Fprintf(response, "kim_ovn_worker_work_backlog{state=%q} %d\n", "PENDING", pending)
		fmt.Fprintf(response, "kim_ovn_worker_work_backlog{state=%q} %d\n", "CLAIMED", claimed)
		fmt.Fprintf(response, "kim_ovn_worker_work_backlog{state=%q} %d\n", "DISPATCH_UNKNOWN", unknown)
		fmt.Fprintf(response, "kim_ovn_worker_work_backlog{state=%q} %d\n", "OBSERVED", observed)
	})
}
