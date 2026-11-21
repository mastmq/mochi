# Why this fork exists

This is [`mochi-mqtt/server`](https://github.com/mochi-mqtt/server), the MQTT library [mast](https://github.com/mastmq/mast) embeds to terminate MQTT. It is MIT licensed and all credit for it belongs upstream: mochi-co and the contributors listed there wrote it, and the SPDX headers on every file still say so.

It is **detached** from the GitHub fork network rather than being a fork in the GitHub sense, so it is a repository of its own with its own issues and its own module path. Issues are on for that reason, and `upstream.yml` files one there when upstream moves.

The fork exists because upstream has stopped merging. The last commit to `mochi-mqtt/server@main` is dated **2025-03-01**, which is also the date of the latest release, `v2.7.9`. There are 47 open pull requests, and [#515, "Is the project still maintained?"](https://github.com/mochi-mqtt/server/issues/515) has been open since August 2026 with no maintainer reply.

We depend on this library, so we carry the patches we need here rather than waiting.

## What this fork carries

| Change | Upstream |
| --- | --- |
| `Clients.GetByListener` no longer takes the read lock recursively | [#488](https://github.com/mochi-mqtt/server/issues/488), fixed identically in [#489](https://github.com/mochi-mqtt/server/pull/489) |
| A peer that hangs up before sending a packet logs at debug, not warn, and the record has a message | not reported upstream |
| The accept loops no longer assign to a shared `err` from inside their goroutines | not reported upstream |
| A client that sets Request Problem Information to 0 still receives the user properties of a PUBLISH | not reported upstream |
| PUBACK, PUBREC, PUBREL and PUBCOMP written while the next inbound packet is already buffered go out in one write | [#552](https://github.com/mochi-mqtt/server/pull/552), carried as is |
| A client whose CONNACK write or inflight resend fails still reaches `OnDisconnect` | not reported upstream |
| `Options.ClientNetWriteTimeout` bounds a write made under the client lock, so a client that stops reading cannot hold that lock, or hang a takeover of its session | [#531](https://github.com/mochi-mqtt/server/pull/531), adapted to #552 |

Nothing else. Every other line is upstream's, and the intent is to keep it that way: a patch here should be one that upstream has already been offered and has not taken.

### The deadlock

`GetByListener` held the read lock and then called `Len`, which takes the read lock again. `sync.RWMutex` documents that a reader arriving after a blocked writer waits for that writer, so a `Lock` landing between the two acquisitions wedges all three permanently — the second `RLock` waits for the writer, the writer waits for the first `RLock` to be released.

In practice the trigger is a client connecting while the server is shutting down: `Server.Close` reaches `GetByListener` through `closeListenerClients`, and `attachClient` reaches `Clients.Delete` for a client id that is already known. We found it as an intermittently hanging test suite, but the production shape is worse — a pod told to terminate never exits, and is eventually SIGKILLed with every connection dropped uncleanly.

`clients_deadlock_test.go` drives the race directly. Against unpatched code it wedges and fails on its 20-second deadline; patched it finishes in well under a second.

### The health-check warnings

`listeners/tcp.go`, `net.go` and `unixsock.go` each ran `l.log.Warn("", "error", err)` for any failure to establish a connection. Two things were wrong with that.

The message was empty, so the record reached production as `{"msg":"","level":"WARN","error":"read connection: EOF"}` — a warning that says nothing.

And an `io.EOF` there is not a failure. It is a peer that opened a connection and closed it without speaking MQTT, which is what a TCP health check does. A Kubernetes `readinessProbe` with `tcpSocket` on the MQTT port and the default ten-second period produces **8,640 of these a day, per pod**, which is how a logger stops being read. Found exactly that way, in a running deployment.

An ordinary hang-up now logs at debug; a failure after the client has started speaking MQTT still warns, because then something really did go wrong.

The same three lines also had a data race: `err = establish(...)` inside the spawned goroutine assigns to the `err` declared by the accept loop, and the loop keeps accepting, so several goroutines write the same variable concurrently.

### User properties withheld from PUBLISH

A client may set Request Problem Information to 0 in CONNECT, and `WritePacket` turns that into `Mods.DisallowProblemInfo`, which `Properties.Encode` applied to user properties on every packet type. The spec exempts PUBLISH, CONNACK and DISCONNECT [MQTT-3.1.2-29], and for a PUBLISH it is not optional: user properties are the publisher's data, and the server must forward them unaltered [MQTT-3.3.2-17]. A subscriber that opted out of diagnostics silently lost the application's metadata on every message.

paho.golang sends 0 whenever a client passes connect properties without setting the flag, which is how mast's own tests found it. Only PUBLISH is exempted here; withholding them from CONNACK and DISCONNECT is still allowed, so that behaviour is left as upstream wrote it.

### Coalesced acks

Every acknowledgement the read loop sends — PUBACK and PUBREC for an inbound PUBLISH, PUBREL for a PUBREC, PUBCOMP for a PUBREL — went through `WritePacket` on its own, and `WritePacket` buffered only while `WriteLoop`'s outbound queue was non-empty. The read loop never queues there, so every ack was a `write(2)` of its own even when the reader already held the next twenty packets. Upstream #552 holds acks in `outbuf` while the next packet is complete in the read buffer, and flushes before the reader would block, after 1ms, when the buffer fills, on any other write, and on DISCONNECT.

It is carried exactly as offered, with only the import paths renamed. mast found the cost under QoS 2 load ([mastmq/mast#18](https://github.com/mastmq/mast/issues/18)): a CPU profile of a node delivering 100k QoS 2 messages a second spent 90% of its time in socket syscalls, 37% of it under `processPubrec`. With the patch, 50 publishers at 100k QoS 2 deliveries a second went from a p50 of 3.3–4.0s, with the publishers falling behind, to 99–191ms with everything delivered.

What it cannot do is batch a connection with nothing behind its ack. A subscriber receiving a message every 10ms answers each one alone, so a QoS 2 delivery still costs the broker two writes and two reads, about 1.6× the CPU of a QoS 1 delivery. That is the protocol, not this code.

### OnDisconnect on every path

`attachClient` adds a client to the client list and runs `OnConnectAuthenticate` before it writes the CONNACK, and on a resumed session it resends the inflight messages after it. When either write failed it returned at once, so `OnDisconnect` never ran, a client with no session to keep stayed in the client list, and its will was not sent. A hook that counted the client when it authenticated counted it for the life of the process.

The resend case is not exotic. It is any device with messages waiting that drops, or is taken over, while they are being resent, which on a mobile link is a matter of time. mast found it as a per-tenant connection limit that filled up and never emptied: [mastmq/mast#32](https://github.com/mastmq/mast/pull/32). Every return after `Clients.Add` now goes through `detachClient`, which is the old tail of `attachClient`: the will on an unclean end, `OnDisconnect`, and the clean-session cleanup.

### Bounded writes

`WritePacket` takes the client lock and writes to the socket inside it, with no write deadline of its own. A client that stops reading with its connection still open fills the socket buffer, and the write blocks with the lock held for as long as the connection lives. Everything else that needs the lock waits behind it: `publishToClient` taking a packet identifier for a QoS 1 delivery, which is before the outbound queue that would have shed the slow client, and `DisconnectClient` when a new connection takes the session over. mast found the takeover case while writing the test for the `OnDisconnect` leak above: a device that reconnects while its old connection still has a backlog being resent never gets its CONNACK.

Upstream #531 adds `Options.ClientNetWriteTimeout`. Zero, the default, changes nothing; set, the deadline is armed and cleared inside the same critical section as the write, a timed-out write in `WriteLoop` stops the client because a partial packet is already on the wire, `refreshDeadline` sets the read deadline only so a client that keeps sending cannot clear the write deadline, and the websocket connection takes the deadline itself because gorilla overwrites one set on the socket. It is carried as offered with one change: #552 gave writes two paths, the direct write and the flush of the coalescing buffer from `writePacket` and from `Read`, so the deadline is armed on each path that writes rather than once at the top of `WritePacket`, which would cost two syscalls for every ack that is only buffered. `TestServerTakeoverOfAClientThatStoppedReading` hangs without the option and passes with it.

The embedder has to set the option. mast does not get the fix by taking the version alone.

## The module path

The module is `github.com/mastmq/mochi/v2`, not `github.com/mochi-mqtt/server/v2`. A detached repository has to declare the path it is actually served from, or `go get` fails on the mismatch.

That rename is mechanical — `go.mod` plus the import line in 50 files — and it is the only change here that is not upstream's code. It does mean a patch sent back upstream needs the rename stripped first. Given upstream has not merged anything since March 2025 that is a theoretical cost, but it is the reason to keep every other line identical.

Versions track upstream's and add a patch digit, so `v2.7.10` is upstream `v2.7.9` plus what is listed above. If upstream ever releases `v2.7.10` of its own the numbers will collide in meaning but not in fact, because the module paths differ.

## Working on it

`upstream` is configured for fetch only, so a stray push cannot reach `mochi-mqtt/server`:

```console
$ git remote -v
origin    git@github.com:mastmq/mochi.git (fetch)
origin    git@github.com:mastmq/mochi.git (push)
upstream  git@github.com:mochi-mqtt/server.git (fetch)
upstream  no-push-use-origin (push)
```

Keep `main` as close to upstream as it can be. If upstream ever revives, the difference should be small enough to send back as a pull request and then drop from here.

Two things were removed rather than carried, because they belong to upstream and not to us: the `docker` job in `.github/workflows/build.yml`, which pushed to the `mochimqtt/server` Docker Hub repository with credentials we do not have, and the Coveralls and Stargazers badges, which would have reported another project's numbers as though they were this one's.

## Keeping it current

Upstream no longer upgrades anything, so this fork has to. Four pieces do it, and each one exists because the step it automates was easy to forget.

**Dependabot** opens one grouped PR a week for Go modules and one for actions, and a monthly one for the example Dockerfile. `dependabot-automerge.yml` turns on auto-merge for minor and patch bumps, so they merge once the required checks pass; a major waits for a person. The required checks on `main` are `test (go.mod)`, `test (stable)` and `govulncheck`. Renaming a job in `build.yml` silently blocks every auto-merge until branch protection names it too.

**Two Go versions in CI.** `go.mod` declares the floor this module promises its importers, and CI tests that floor. CI also tests the newest stable Go, because mast builds with a newer Go than the floor, and a Go release that breaks this code should fail here first. Raise the `go` line in `go.mod` only when the code needs a newer feature: every raise forces it on every importer.

**`upstream.yml`** fetches `mochi-mqtt/server` every Monday and opens an issue if upstream's `main` has commits this fork has not merged. If upstream revives, merge it, keep the patches in the table above, and offer them back.

**A release is still a person's decision.** A merged bump reaches nobody until it is tagged. Tag when a bump changes a module mast compiles (the packet, listener and hook code, not the storage hooks), when `govulncheck` reports something reachable, or at least monthly when bumps have piled up. Bump the version string in `server.go` in the same commit, then push the `v2.x.y` tag. mast's Dependabot keeps `github.com/mastmq/mochi/*` out of its grouped PR, so the new tag arrives in mast as a PR of its own, where the cluster suite decides whether it is safe.
