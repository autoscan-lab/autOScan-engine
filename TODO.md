# TODO

## Features

### Multi-process scenarios on the cloud `/grade`

The engine supports `policy.Run.MultiProcess.TestScenarios` via `Executor.ExecuteMultiProcessScenario`. The HTTP server doesn't call it.

batch-and-return works (the executor takes a `nil` progress callback and returns the full `MultiProcessResult`). Real blockers:

1. Per-submission temp working directories so FIFOs/Unix sockets in `/tmp` don't collide across parallel `/grade` calls
2. Concurrency cap or bigger Fly VM — the current `shared-cpu-1x / 1 GB` config is too small once N processes × M scenarios scales up

Once those are handled, add a `include_multi_process=1` form field to
`/grade`.
