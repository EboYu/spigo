// Package store simulates a generic business logic microservice
// Takes incoming traffic and calls into dependent microservices in a single zone
package store

import (
	"fmt"
	"github.com/adrianco/spigo/archaius"
	"github.com/adrianco/spigo/collect"
	"github.com/adrianco/spigo/flow"
	"github.com/adrianco/spigo/gotocol"
	"github.com/adrianco/spigo/names"
	"time"
)

// Start store, all configuration and state is sent via messages
func Start(listener chan gotocol.Message) {
	// remember the channel to talk to microservices
	microservices := make(map[string]chan gotocol.Message)
	dependencies := make(map[string]time.Time) // dependent services and time last updated
	store := make(map[string]string, 4)        // key value store
	store["why?"] = "because..."
	var netflixoss chan gotocol.Message     // remember creator and how to talk back to incoming requests
	var name string                                    // remember my name
	eureka := make(map[string]chan gotocol.Message, 3) // service registry per zone
	hist := collect.NewHist("")
	ep, _ := time.ParseDuration(archaius.Conf.EurekaPoll)
	eurekaTicker := time.NewTicker(ep)
	for {
		select {
		case msg := <-listener:
			flow.Instrument(msg, name, hist)
			switch msg.Imposition {
			case gotocol.Hello:
				if name == "" {
					// if I don't have a name yet remember what I've been named
					netflixoss = msg.ResponseChan // remember how to talk to my namer
					name = msg.Intention          // message body is my name
					hist = collect.NewHist(name)
				}
			case gotocol.Inform:
				eureka[msg.Intention] = gotocol.InformHandler(msg, name, listener)
			case gotocol.NameDrop: // cross zone = true
				gotocol.NameDropHandler(&dependencies, &microservices, msg, name, listener, eureka, true)
			case gotocol.Forget:
				// forget a buddy
				gotocol.ForgetHandler(&dependencies, &microservices, msg)
			case gotocol.GetRequest:
				// return any stored value for this key
				outmsg := gotocol.Message{gotocol.GetResponse, listener, time.Now(), msg.Ctx, store[msg.Intention]}
				flow.AnnotateSend(outmsg, name)
				outmsg.GoSend(msg.ResponseChan)
			case gotocol.GetResponse:
				// return path from a request, send payload back up (not currently used)
			case gotocol.Put:
				// set a key value pair and replicate globally
				var key, value string
				fmt.Sscanf(msg.Intention, "%s%s", &key, &value)
				if key != "" && value != "" {
					store[key] = value
					// duplicate the request on to all connected store nodes
					if len(microservices) > 0 {
						// replicate request
						for _, c := range microservices {
							outmsg := gotocol.Message{gotocol.Replicate, listener, time.Now(), msg.Ctx.NewParent(), msg.Intention}
							flow.AnnotateSend(outmsg, name)
							outmsg.GoSend(c)
						}
					}
				}
			case gotocol.Replicate:
				// Replicate is only used between store nodes
				// end point for a request
				var key, value string
				fmt.Sscanf(msg.Intention, "%s%s", &key, &value)
				// log.Printf("store: %v:%v", key, value)
				if key != "" && value != "" {
					store[key] = value
				}
				// name looks like: netflixoss.us-east-1.zoneC.mysql.store.mysql1
				myregion := names.Region(name)
				//log.Printf("%v: %v\n", name, myregion)
				// find if this was a cross region Replicate
				for n, c := range microservices {
					// find the name matching incoming request channel
					if c == msg.ResponseChan {
						if myregion != names.Region(n) {
							// Replicate from out of region needs to be Replicated only to other zones in this Region
							for nz, cz := range microservices {
								if myregion == names.Region(nz) {
									//log.Printf("%v rep to: %v\n", name, nz)
									outmsg := gotocol.Message{gotocol.Replicate, listener, time.Now(), msg.Ctx.NewParent(), msg.Intention}
									flow.AnnotateSend(outmsg, name)
									outmsg.GoSend(cz)								}
							}
						}
					}
				}
			case gotocol.Goodbye:
				gotocol.Message{gotocol.Goodbye, nil, time.Now(), gotocol.NilContext, name}.GoSend(netflixoss)
				return
			}
		case <-eurekaTicker.C: // check to see if any new dependencies have appeared
			for dep, _ := range dependencies {
				for _, ch := range eureka {
					ch <- gotocol.Message{gotocol.GetRequest, listener, time.Now(), gotocol.NilContext, dep}
				}
			}
		}
	}
}
