# Schedules

Add the annotations to a Deployment, StatefulSet or CronJob (or one of your [other kinds](other-kinds.md)):

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: my-app
  annotations:
    curfew/sleep: "0 20 * * 1-5"     # scale to 0 at 20:00, Monday to Friday
    curfew/wake: "0 8 * * 1-5"       # restore at 08:00, Monday to Friday
    curfew/timezone: "Europe/Paris"  # optional, default UTC
```

| Annotation | Required | Format |
|---|---|---|
| `curfew/sleep` | yes | Standard 5-field cron (`minute hour day-of-month month day-of-week`) or a descriptor like `@daily` |
| `curfew/wake` | yes | Same as `sleep` |
| `curfew/timezone` | no | IANA time zone name, e.g. `America/New_York`. Default `UTC`. |
| `curfew/skip-dates` | no | Comma-separated dates (`YYYY-MM-DD`). No sleep or wake on those days (see [Holidays](holidays.md)). |
| `curfew/awake-until` | no | RFC 3339 time, e.g. `2026-10-28T08:00:00+01:00`. Stay awake until then (see [Keep awake](keep-awake.md)). |
| `curfew/wave` | no | Integer, default 0. Order of sleep and wake within a namespace (see [Waves](waves.md)). |

Both schedules are read in the given time zone.

The example above sleeps from Friday 20:00 until Monday 08:00: after Friday's sleep, the next wake time is Monday, so the workload stays down all weekend.

## How curfew decides

At any moment curfew looks at which comes next, the next sleep or the next wake:

- **Wake comes first:** the workload should be asleep now.
- **Sleep comes first:** the workload should be awake now.

It then schedules its next check for exactly that time. Adding the annotations during a sleep window scales the workload down right away.

## Waking up

- At wake time, curfew restores the replica count it stored at sleep time.
- **Removing the annotations** wakes a sleeping workload right away.
- A workload that was already at 0 at sleep time stays at 0.

[Behavior](../behavior.md) covers the details, such as scaling a sleeping workload by hand or deleting it.
