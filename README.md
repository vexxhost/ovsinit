# `ovsinit`

`ovsinit` is a utility for safely restarting Open vSwitch daemons (such as
`ovs-vswitchd`, `ovsdb-server`, `ovn-controller`, etc.) in Kubernetes
environments with minimal dataplane disruption.

## Problem

Traditional Kubernetes rollouts stop the old pod before starting the new one.
If the container image isn’t already pulled or startup is slow, the dataplane
can be down for several seconds. This leads to:

- Dropped packets
- Connection failures
- Noticeable cutovers in network services

Even with `RollingUpdate`, Kubernetes typically terminates first, starts
second, which is not ideal for critical network daemons.

## Solution

`ovsinit` enables in-place, low-downtime daemon restarts by leveraging
`appctl` for graceful shutdowns and `syscall.Exec` to replace the process
without changing its PID. This minimizes disruption during upgrades or
restarts.  It is designed to work with rollout strategies in the example
below.

```yaml
updateStrategy:
  type: RollingUpdate
  rollingUpdate:
    maxUnavailable: 0
    maxSurge: 1
```

This ensures the new pod is fully up and ready to take over before the old
one exits.

### How It Works

When `ovsinit` is used as the container entrypoint, it:

1. Detects whether the target OVS daemon is already running.
2. If so, uses `appctl` `exit` to gracefully stop it.
3. Waits for the process to terminate.
4. Uses `syscall.Exec` to start the new daemon in place

This provides a smooth handoff between instances without killing the dataplane
prematurely.

### Why It Matters

By starting the new pod _before_ terminating the old one, and letting `ovsinit`
handle the switchover internally, you:

- Avoid multi-second dataplane outages
- Skip image-pull delays
- Keep `ovsdb-server`/`ovs-vswitchd` state tightly controlled
- Ensure traffic disruption is measured in hundreds of milliseconds, not seconds

In testing, `ovsinit` consistently reduced restart downtime to a level that is
typically invisible to end users.
