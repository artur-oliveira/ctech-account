package deletion

import "time"

// WorkerInterval is how often the due index is polled (by the supervisor in cmd/api).
const WorkerInterval = time.Minute
