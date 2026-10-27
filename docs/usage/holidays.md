# Holidays

Cron can't say "not on public holidays". List those days instead, next to the schedule:

```yaml
metadata:
  annotations:
    curfew/sleep: "0 20 * * 1-5"
    curfew/wake: "0 8 * * 1-5"
    curfew/skip-dates: "2026-12-24,2026-12-25,2027-01-01"
```

No sleep or wake happens on a listed day, so the workload keeps the state it was in when the day started. With this schedule, a workload stays asleep:

- from Wednesday 23 December at 20:00 until Monday 28 December at 08:00,
- and from Thursday 31 December at 20:00 until Monday 4 January at 08:00.

## Rules

- Dates are read in the schedule's `curfew/timezone`.
- `curfew/skip-dates` belongs to the schedule. On a namespace, it applies to the workloads that follow the [namespace's schedule](namespaces.md). A workload with its own `curfew/sleep` needs its own `curfew/skip-dates`.
- Exact dates only: no ranges and no yearly repeats, so update the list each year.
- An invalid date is logged and the workload is left as it is.

To keep workloads **awake** on a day they would sleep, use [Keep awake](keep-awake.md) instead.
