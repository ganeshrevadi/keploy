package connection

import (
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"go.uber.org/zap"

	"go.keploy.io/server/pkg"
	"go.keploy.io/server/pkg/hooks/structs"
	"go.keploy.io/server/pkg/models"
	"go.keploy.io/server/pkg/platform"
)

var Emoji = "\U0001F430" + " Keploy:"

// Factory is a routine-safe container that holds a trackers with unique ID, and able to create new tracker.
type Factory struct {
	connections         map[structs.ConnID]*Tracker
	inactivityThreshold time.Duration
	mutex               *sync.RWMutex
	logger              *zap.Logger
}

// NewFactory creates a new instance of the factory.
func NewFactory(inactivityThreshold time.Duration, logger *zap.Logger) *Factory {
	return &Factory{
		connections:         make(map[structs.ConnID]*Tracker),
		mutex:               &sync.RWMutex{},
		inactivityThreshold: inactivityThreshold,
		logger:              logger,
	}
}

// func (factory *Factory) HandleReadyConnections(k *keploy.Keploy) {
// func (factory *Factory) HandleReadyConnections(path string, db platform.TestCaseDB, getDeps func() []*models.Mock, resetDeps func() int) {
func (factory *Factory) HandleReadyConnections(db platform.TestCaseDB) {

	factory.mutex.Lock()
	defer factory.mutex.Unlock()
	var trackersToDelete []structs.ConnID
	for connID, tracker := range factory.connections {
		if tracker.IsComplete() {
			trackersToDelete = append(trackersToDelete, connID)
			if len(tracker.SentBuf) == 0 && len(tracker.RecvBuf) == 0 {
				continue
			}
			parsedHttpReq, err := pkg.ParseHTTPRequest(tracker.RecvBuf)
			if err != nil {
				factory.logger.Error("failed to parse the http request from byte array", zap.Error(err))
				continue
			}
			parsedHttpRes, err := pkg.ParseHTTPResponse(tracker.SentBuf, parsedHttpReq)
			if err != nil {
				factory.logger.Error("failed to parse the http response from byte array", zap.Error(err))
				continue
			}

			switch models.GetMode() {
			case models.MODE_RECORD:
				// capture the ingress call for record cmd
				factory.logger.Debug("capturing ingress call from tracker in record mode")
				capture(db, parsedHttpReq, parsedHttpRes, factory.logger)
			case models.MODE_TEST:
				factory.logger.Debug("skipping tracker in test mode")
			default:
				factory.logger.Warn("Keploy mode is not set to record or test. Tracker is being skipped.",
					zap.Any("current mode", models.GetMode()))
			}

		} else if tracker.Malformed() || tracker.IsInactive(factory.inactivityThreshold) {
			trackersToDelete = append(trackersToDelete, connID)
		}
	}

	// Delete all the processed trackers.
	for _, key := range trackersToDelete {
		delete(factory.connections, key)
	}
}

// GetOrCreate returns a tracker that related to the given connection and transaction ids. If there is no such tracker
// we create a new one.
func (factory *Factory) GetOrCreate(connectionID structs.ConnID) *Tracker {
	factory.mutex.Lock()
	defer factory.mutex.Unlock()
	tracker, ok := factory.connections[connectionID]
	if !ok {
		factory.connections[connectionID] = NewTracker(connectionID, factory.logger)
		return factory.connections[connectionID]
	}
	return tracker
}

func capture(db platform.TestCaseDB, req *http.Request, resp *http.Response, logger *zap.Logger) {
	// meta := map[string]string{
	// 	"method": req.Method,
	// }
	// httpMock := &models.Mock{
	// 	Version: models.V1Beta2,
	// 	Name:    "",
	// 	Kind:    models.HTTP,
	// }

	reqBody, err := io.ReadAll(req.Body)
	if err != nil {
		logger.Error("failed to read the http request body", zap.Error(err))
		return
	}

	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Error("failed to read the http response body", zap.Error(err))
		return
	}

	// Encode the message into yaml
	// mocks := getDeps()
	// mockIds := []string{}
	// for i, v := range mocks {
	// 	if v != nil {
	// 		mockIds = append(mockIds, fmt.Sprintf("%v-%v", v.Name, i))
	// 	}
	// }

	// err = db.Insert(httpMock, getDeps())
	err = db.WriteTestcase(&models.TestCase{
		Version: models.V1Beta2,
		Name:    "",
		Kind:    models.HTTP,
		Created: time.Now().Unix(),
		HttpReq: models.HttpReq{
			Method:     models.Method(req.Method),
			ProtoMajor: req.ProtoMajor,
			ProtoMinor: req.ProtoMinor,
			// URL:        req.URL.String(),
			// URL: fmt.Sprintf("%s://%s%s?%s", req.URL.Scheme, req.Host, req.URL.Path, req.URL.RawQuery),
			URL: fmt.Sprintf("http://%s%s", req.Host, req.URL.RequestURI()),
			//  URL: string(b),
			Header:    pkg.ToYamlHttpHeader(req.Header),
			Body:      string(reqBody),
			URLParams: pkg.UrlParams(req),
		},
		HttpResp: models.HttpResp{
			StatusCode: resp.StatusCode,
			Header:     pkg.ToYamlHttpHeader(resp.Header),
			Body:       string(respBody),
		},
		// Mocks: mocks,
	})
	if err != nil {
		logger.Error("failed to record the ingress requests", zap.Error(err))
		return
	}
}
