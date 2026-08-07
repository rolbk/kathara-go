// The two clock seams the renderers need. They are variables rather than direct
// calls to `time` so that a test can pin the timestamp a table prints — the
// only nondeterminism in `create_lab_table`'s output is `datetime.now()`.

package main

import "time"

// nowFunc is `datetime.now()` for the two table titles.
var nowFunc = time.Now

// watchInterval is the refresh period of `list --watch`. `rich.live.Live` is
// started at `refresh_per_second=12.5` and dropped to 1 right after the initial
// spinner (`ListCommand.py:78-80`), so one second is the rate the user actually
// sees.
var watchInterval = time.Second

// tickerC is the delay between two redraws of the watch loop.
func tickerC() <-chan time.Time { return time.After(watchInterval) }
