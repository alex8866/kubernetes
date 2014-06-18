/*
Copyright 2014 Google Inc. All rights reserved.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package apiserver

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"runtime/debug"
	"strings"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/labels"
)

// RESTStorage is a generic interface for RESTful storage services
type RESTStorage interface {
	List(labels.Query) (interface{}, error)
	Get(id string) (interface{}, error)
	Delete(id string) error
	Extract(body string) (interface{}, error)
	Create(interface{}) error
	Update(interface{}) error
}

// Status is a return value for calls that don't return other objects
type Status struct {
	Success bool
}

// ApiServer is an HTTPHandler that delegates to RESTStorage objects.
// It handles URLs of the form:
// ${prefix}/${storage_key}[/${object_name}]
// Where 'prefix' is an arbitrary string, and 'storage_key' points to a RESTStorage object stored in storage.
//
// TODO: consider migrating this to go-restful which is a more full-featured version of the same thing.
type ApiServer struct {
	prefix  string
	storage map[string]RESTStorage
}

// New creates a new ApiServer object.
// 'storage' contains a map of handlers.
// 'prefix' is the hosting path prefix.
func New(storage map[string]RESTStorage, prefix string) *ApiServer {
	return &ApiServer{
		storage: storage,
		prefix:  prefix,
	}
}

func (server *ApiServer) handleIndex(w http.ResponseWriter) {
	w.WriteHeader(http.StatusOK)
	// TODO: serve this out of a file?
	data := "<html><body>Welcome to Kubernetes</body></html>"
	fmt.Fprint(w, data)
}

// HTTP Handler interface
func (server *ApiServer) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	defer func() {
		if x := recover(); x != nil {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, "apiserver panic. Look in log for details.")
			log.Printf("ApiServer panic'd: %#v\n%s\n", x, debug.Stack())
		}
	}()
	log.Printf("%s %s", req.Method, req.RequestURI)
	url, err := url.ParseRequestURI(req.RequestURI)
	if err != nil {
		server.error(err, w)
		return
	}
	if url.Path == "/index.html" || url.Path == "/" || url.Path == "" {
		server.handleIndex(w)
		return
	}
	if !strings.HasPrefix(url.Path, server.prefix) {
		server.notFound(req, w)
		return
	}
	requestParts := strings.Split(url.Path[len(server.prefix):], "/")[1:]
	if len(requestParts) < 1 {
		server.notFound(req, w)
		return
	}
	storage := server.storage[requestParts[0]]
	if storage == nil {
		server.notFound(req, w)
		return
	} else {
		server.handleREST(requestParts, url, req, w, storage)
	}
}

func (server *ApiServer) notFound(req *http.Request, w http.ResponseWriter) {
	w.WriteHeader(404)
	fmt.Fprintf(w, "Not Found: %#v", req)
}

func (server *ApiServer) write(statusCode int, object interface{}, w http.ResponseWriter) {
	w.WriteHeader(statusCode)
	output, err := json.MarshalIndent(object, "", "    ")
	if err != nil {
		server.error(err, w)
		return
	}
	w.Write(output)
}

func (server *ApiServer) error(err error, w http.ResponseWriter) {
	w.WriteHeader(500)
	fmt.Fprintf(w, "Internal Error: %#v", err)
}

func (server *ApiServer) readBody(req *http.Request) (string, error) {
	defer req.Body.Close()
	body, err := ioutil.ReadAll(req.Body)
	return string(body), err
}

// handleREST is the main dispatcher for the server.  It switches on the HTTP method, and then
// on path length, according to the following table:
//   Method     Path          Action
//   GET        /foo          list
//   GET        /foo/bar      get 'bar'
//   POST       /foo          create
//   PUT        /foo/bar      update 'bar'
//   DELETE     /foo/bar      delete 'bar'
// Returns 404 if the method/pattern doesn't match one of these entries
func (server *ApiServer) handleREST(parts []string, requestUrl *url.URL, req *http.Request, w http.ResponseWriter, storage RESTStorage) {
	switch req.Method {
	case "GET":
		switch len(parts) {
		case 1:
			query, err := labels.ParseQuery(requestUrl.Query().Get("labels"))
			if err != nil {
				server.error(err, w)
				return
			}
			controllers, err := storage.List(query)
			if err != nil {
				server.error(err, w)
				return
			}
			server.write(200, controllers, w)
		case 2:
			item, err := storage.Get(parts[1])
			if err != nil {
				server.error(err, w)
				return
			}
			if item == nil {
				server.notFound(req, w)
				return
			}
			server.write(200, item, w)
		default:
			server.notFound(req, w)
		}
		return
	case "POST":
		if len(parts) != 1 {
			server.notFound(req, w)
			return
		}
		body, err := server.readBody(req)
		if err != nil {
			server.error(err, w)
			return
		}
		obj, err := storage.Extract(body)
		if err != nil {
			server.error(err, w)
			return
		}
		err = storage.Create(obj)
		if err != nil {
			server.error(err, w)
		} else {
			server.write(200, obj, w)
		}
		return
	case "DELETE":
		if len(parts) != 2 {
			server.notFound(req, w)
			return
		}
		err := storage.Delete(parts[1])
		if err != nil {
			server.error(err, w)
			return
		}
		server.write(200, Status{Success: true}, w)
		return
	case "PUT":
		if len(parts) != 2 {
			server.notFound(req, w)
			return
		}
		body, err := server.readBody(req)
		if err != nil {
			server.error(err, w)
		}
		obj, err := storage.Extract(body)
		if err != nil {
			server.error(err, w)
			return
		}
		err = storage.Update(obj)
		if err != nil {
			server.error(err, w)
			return
		}
		server.write(200, obj, w)
		return
	default:
		server.notFound(req, w)
	}
}
