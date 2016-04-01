// Copyright 2015 CoreOS, Inc
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"flag"
	"os"
	"os/signal"
	"sync"
	"syscall"

	log "github.com/Sirupsen/logrus"

	"github.com/coreos-inc/jwtproxy/config"
	"github.com/coreos-inc/jwtproxy/jwt"
	"github.com/coreos-inc/jwtproxy/proxy"

	_ "github.com/coreos-inc/jwtproxy/jwt/keyserver/keyregistry"
	_ "github.com/coreos-inc/jwtproxy/jwt/keyserver/preshared"
	_ "github.com/coreos-inc/jwtproxy/jwt/noncestorage/local"
	_ "github.com/coreos-inc/jwtproxy/jwt/privatekey/autogenerated"
	_ "github.com/coreos-inc/jwtproxy/jwt/privatekey/preshared"
)

func main() {
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	flagConfigPath := flag.String("config", "", "Load configuration from the specified yaml file.")
	flagLogLevel := flag.String("log-level", "info", "Define the logging level.")
	flag.Parse()

	// Load configuration.
	config, err := config.Load(*flagConfigPath)
	if err != nil {
		flag.Usage()
		log.Fatalf("Failed to load configuration: %s", err)
	}

	// Initialize logging system.
	level, err := log.ParseLevel(*flagLogLevel)
	if err != nil {
		log.Fatalf("Failed to parse the log level: %s", err)
	}
	log.SetLevel(level)

	// Create JWT proxy handlers.
	signer, err := jwt.NewJWTSignerHandler(config.SignerProxy.Signer)
	if err != nil {
		log.Errorf("Failed to create JWT signer: %s", err)
		return
	}
	defer signer.Stop()

	verifier, err := jwt.NewJWTVerifierHandler(config.VerifierProxy.Verifier)
	if err != nil {
		log.Errorf("Failed to create JWT verifier: %s", err)
		return
	}
	defer verifier.Stop()

	// Create forward and reverse proxies.
	forwardProxy, err := proxy.NewProxy(signer.Handler, config.SignerProxy.CAKeyFile, config.SignerProxy.CACrtFile, config.SignerProxy.TrustedCertificates)
	if err != nil {
		log.Errorf("Failed to create forward proxy: %s", err)
		return
	}

	reverseProxy, err := proxy.NewReverseProxy(verifier.Handler)
	if err != nil {
		log.Errorf("Failed to create reverse proxy: %s", err)
		return
	}

	// Start proxies.
	var proxiesWG sync.WaitGroup
	startProxy(&proxiesWG, config.SignerProxy.ListenAddr, "", "", "forward", forwardProxy)
	startProxy(&proxiesWG, config.VerifierProxy.ListenAddr, config.VerifierProxy.CrtFile, config.VerifierProxy.KeyFile, "reverse", reverseProxy)

	// Wait for stop signal.
	shutdown := make(chan os.Signal)
	signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM)
	<-shutdown
	log.Info("Received stop signal. Stopping gracefully...")

	// Stop proxies gracefully.
	forwardProxy.Stop(config.SignerProxy.ShutdownTimeout)
	reverseProxy.Stop(config.VerifierProxy.ShutdownTimeout)
	proxiesWG.Wait()

	// Now that proxies are stopped, the signer and the verifier can be stopped, along with all their
	// subsystems. This is done by the defer statements above.
}

func startProxy(wg *sync.WaitGroup, listenAddr, crtFile, keyFile string, proxyName string, proxy *proxy.Proxy) {
	wg.Add(1)
	go func() {
		defer wg.Done()

		log.Infof("Starting %s proxy (Listening on '%s')", proxyName, listenAddr)
		if err := proxy.Serve(listenAddr, crtFile, keyFile); err != nil {
			log.Errorf("Failed to start %s proxy: %s", proxyName, err)
		}
	}()
}
