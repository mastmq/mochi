# Why this fork exists

This is [`mochi-mqtt/server`](https://github.com/mochi-mqtt/server), the MQTT library [mast](https://github.com/mastmq/mast) embeds to terminate MQTT. It is MIT licensed and all credit for it belongs upstream: mochi-co and the contributors listed there wrote it, and the SPDX headers on every file still say so.

It is **detached** from the GitHub fork network rather than being a fork in the GitHub sense, so it is a repository of its own with its own issues and its own module path.

The fork exists because upstream has stopped merging. The last commit to `mochi-mqtt/server@main` is dated **2025-03-01**, which is also the date of the latest release, `v2.7.9`. There are 47 open pull requests, and [#515, "Is the project still maintained?"](https://github.com/mochi-mqtt/server/issues/515) has been open since August 2026 with no maintainer reply.

We depend on this library, so we carry the patches we need here rather than waiting.

## What this fork carries

| Change | Upstream |
| --- | --- |
| `Clients.GetByListener` no longer takes the read lock recursively | [#488](https://github.com/mochi-mqtt/server/issues/488), fixed identically in [#489](https://github.com/mochi-mqtt/server/pull/489) |

Nothing else. Every other line is upstream's, and the intent is to keep it that way: a patch here should be one that upstream has already been offered and has not taken.

### The deadlock

`GetByListener` held the read lock and then called `Len`, which takes the read lock again. `sync.RWMutex` documents that a reader arriving after a blocked writer waits for that writer, so a `Lock` landing between the two acquisitions wedges all three permanently — the second `RLock` waits for the writer, the writer waits for the first `RLock` to be released.

In practice the trigger is a client connecting while the server is shutting down: `Server.Close` reaches `GetByListener` through `closeListenerClients`, and `attachClient` reaches `Clients.Delete` for a client id that is already known. We found it as an intermittently hanging test suite, but the production shape is worse — a pod told to terminate never exits, and is eventually SIGKILLed with every connection dropped uncleanly.

`clients_deadlock_test.go` drives the race directly. Against unpatched code it wedges and fails on its 20-second deadline; patched it finishes in well under a second.

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
