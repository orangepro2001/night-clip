using Xunit;

// The Go core lives in this process (cgo, its own signal handlers and threads). Running it while other tests allocate
// and trigger garbage collections in parallel made the test host crash now and then; one test at a time is stable
// and the whole suite still finishes in seconds.
[assembly: CollectionBehavior(DisableTestParallelization = true)]
