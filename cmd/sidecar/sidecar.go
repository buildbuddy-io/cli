package main

import (
	"flag"
	"log"
	"net"
	"strings"

	"github.com/buildbuddy-io/buildbuddy-cli/cache_proxy"
	"github.com/buildbuddy-io/buildbuddy-cli/devnull"
	"github.com/buildbuddy-io/buildbuddy/server/build_event_protocol/build_event_proxy"
	"github.com/buildbuddy-io/buildbuddy/server/build_event_protocol/build_event_server"
	"github.com/buildbuddy-io/buildbuddy/server/config"
	"github.com/buildbuddy-io/buildbuddy/server/nullauth"
	"github.com/buildbuddy-io/buildbuddy/server/real_environment"
	"github.com/buildbuddy-io/buildbuddy/server/util/grpc_client"
	"github.com/buildbuddy-io/buildbuddy/server/util/grpc_server"
	"github.com/buildbuddy-io/buildbuddy/server/util/healthcheck"

	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	pepb "github.com/buildbuddy-io/buildbuddy/proto/publish_build_event"
	repb "github.com/buildbuddy-io/buildbuddy/proto/remote_execution"
	rpcfilters "github.com/buildbuddy-io/buildbuddy/server/rpc/filters"
	bspb "google.golang.org/genproto/googleapis/bytestream"
)

var (
	serverType = flag.String("server_type", "sidecar", "The server type to match on health checks")

	listenAddr  = flag.String("listen_addr", "localhost:1991", "Local address to listen on.")
	besBackend  = flag.String("bes_backend", "grpcs://cloud.buildbuddy.io:443", "Server address to proxy build events to.")
	remoteCache = flag.String("remote_cache", "grpcs://cloud.buildbuddy.io:443", "Server address to cache events to.")
)

func main() {
	flag.Parse()
	configurator, err := config.NewConfigurator("")
	if err != nil {
		log.Fatalf("Error initializing Configurator: %s", err.Error())
	}
	healthChecker := healthcheck.NewHealthChecker(*serverType)
	env := real_environment.NewRealEnv(configurator, healthChecker)
	env.SetAuthenticator(&nullauth.NullAuthenticator{})
	env.SetBuildEventHandler(&devnull.BuildEventHandler{})

	var lis net.Listener
	if strings.HasPrefix(*listenAddr, "unix://") {
		sockPath := strings.TrimPrefix(*listenAddr, "unix://")
		lis, err = net.Listen("unix", sockPath)
	} else {
		lis, err = net.Listen("tcp", *listenAddr)
	}
	if err != nil {
		log.Fatalf("Failed to listen: %s", err.Error())
	}
	log.Printf("gRPC listening on %q", *listenAddr)
	grpcOptions := []grpc.ServerOption{
		rpcfilters.GetUnaryInterceptor(env),
		rpcfilters.GetStreamInterceptor(env),
		grpc.MaxRecvMsgSize(env.GetConfigurator().GetGRPCMaxRecvMsgSizeBytes()),
	}
	grpcServer := grpc.NewServer(grpcOptions...)
	reflection.Register(grpcServer)
	env.GetHealthChecker().RegisterShutdownFunction(grpc_server.GRPCShutdownFunc(grpcServer))

	if *besBackend != "" {
		buildEventProxyClients := make([]pepb.PublishBuildEventClient, 0)
		buildEventProxyClients = append(buildEventProxyClients, build_event_proxy.NewBuildEventProxyClient(*besBackend))
		log.Printf("Proxy: forwarding build events to: %q", *besBackend)
		env.SetBuildEventProxyClients(buildEventProxyClients)

		// Register to handle build event protocol messages.
		buildEventServer, err := build_event_server.NewBuildEventProtocolServer(env)
		if err != nil {
			log.Fatalf("Error initializing BuildEventProtocolServer: %s", err.Error())
		}
		pepb.RegisterPublishBuildEventServer(grpcServer, buildEventServer)
	}

	if *remoteCache != "" {
		conn, err := grpc_client.DialTarget(*remoteCache)
		if err != nil {
			log.Fatalf("Error dialing remote cache: %s", err.Error())
		}
		cacheProxy, err := cache_proxy.NewCacheProxy(conn)
		if err != nil {
			log.Fatalf("Error initializing cache proxy: %s", err.Error())
		}
		bspb.RegisterByteStreamServer(grpcServer, cacheProxy)
		repb.RegisterActionCacheServer(grpcServer, cacheProxy)
		repb.RegisterContentAddressableStorageServer(grpcServer, cacheProxy)
		repb.RegisterCapabilitiesServer(grpcServer, cacheProxy)
	}

	if *besBackend != "" || *remoteCache != "" {
		grpcServer.Serve(lis)
	} else {
		log.Fatal("No services configured. At least one of --bes_backend or --remote_cache must be provided!")
	}
}
