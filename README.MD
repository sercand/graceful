Golang graceful close utility.

It listens SIGINT and SIGTERM then calls GracefulStop function of given objects

## USAGE

```go

type my struct{db: *sql.DB}

// implements graceful.Stoppable
func (m *my) GracefulStop(){
    m.db.Close()
}

func serve(grpcPort, httpPort string, x *my) error {
    return <-graceful.ServeAndStopOnSignals(
        graceful.NewServable(grpcPort, grpcServer),   // Grpc Server
        x,                                            // Your struct
        graceful.ListenAndServe(httpPort, httpServer),  // http server
	)
}

// OR
// StopOnSignals calls GracefulStop() on signals
go graceful.StopOnSignals(a, grpcServer, c)

```
