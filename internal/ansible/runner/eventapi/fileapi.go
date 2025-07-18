// Copyright 2018 The Operator-SDK Authors
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

package eventapi

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/go-logr/logr"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// FileEventReceiver watches ansible-runner artifact files for events
type FileEventReceiver struct {
	// Events is the channel used to send JobEvents back to the runner
	Events chan JobEvent

	// ArtifactPath is the path where ansible-runner writes artifact files
	ArtifactPath string

	// stopped indicates if this receiver has permanently stopped receiving events
	stopped bool

	// mutex controls access to the "stopped" bool
	mutex sync.RWMutex

	// ident is the unique identifier for a particular run of ansible-runner
	ident string

	// logger holds a logger that has some fields already set
	logger logr.Logger

	// errChan is a channel for errors
	errChan chan<- error

	// ctx is the context for cancellation
	ctx context.Context
}

// NewFileEventReceiver creates a new file-based event receiver
func NewFileEventReceiver(ident string, artifactPath string, errChan chan<- error) (*FileEventReceiver, error) {
	receiver := &FileEventReceiver{
		Events:       make(chan JobEvent, 1000),
		ArtifactPath: artifactPath,
		ident:        ident,
		logger:       logf.Log.WithName("fileapi").WithValues("job", ident),
		errChan:      errChan,
		ctx:          context.Background(), // Placeholder, will be updated
	}

	// Start watching for file changes
	go receiver.watchJobEvents()

	return receiver, nil
}

// watchJobEvents monitors the job_events directory for new files
func (f *FileEventReceiver) watchJobEvents() {
	defer close(f.Events)

	// Watch the job_events directory, not individual files
	jobEventsDir := filepath.Join(f.ArtifactPath, f.ident, "job_events")

	// Ensure directory exists before watching
	if err := os.MkdirAll(jobEventsDir, 0755); err != nil {
		f.errChan <- fmt.Errorf("failed to create job_events directory: %v", err)
		return
	}

	// Process any existing files first
	f.processExistingFiles(jobEventsDir)

	// Watch for new files
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		f.errChan <- fmt.Errorf("failed to create file watcher: %v", err)
		return
	}
	defer watcher.Close()

	if err := watcher.Add(jobEventsDir); err != nil {
		f.errChan <- fmt.Errorf("failed to watch job_events directory: %v", err)
		return
	}

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			// Only process new JSON files
			if event.Op&fsnotify.Create == fsnotify.Create &&
				filepath.Ext(event.Name) == ".json" {
				f.processEventFile(event.Name)
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			f.errChan <- fmt.Errorf("file watcher error: %v", err)
		case <-f.ctx.Done():
			return
		}
	}
}

// processExistingFiles processes any existing event files in the directory
func (f *FileEventReceiver) processExistingFiles(jobEventsDir string) {
	files, err := os.ReadDir(jobEventsDir)
	if err != nil {
		// Directory might not exist yet, which is OK
		return
	}

	// Sort files by name to process in order (ansible-runner prefixes with sequence number)
	var jsonFiles []string
	for _, file := range files {
		if !file.IsDir() && filepath.Ext(file.Name()) == ".json" {
			jsonFiles = append(jsonFiles, filepath.Join(jobEventsDir, file.Name()))
		}
	}

	// Process in order
	for _, filePath := range jsonFiles {
		f.processEventFile(filePath)
	}
}

// processEventFile reads and parses a single event file
func (r *FileEventReceiver) processEventFile(filename string) bool {
	file, err := os.Open(filename)
	if err != nil {
		r.logger.V(2).Info("Could not open event file (may not be ready)", "file", filename)
		return false
	}
	defer file.Close()

	// Parse JSON event from file
	var event JobEvent
	decoder := json.NewDecoder(file)
	if err := decoder.Decode(&event); err != nil {
		// Skip files that aren't valid JSON (might be partial writes)
		r.logger.V(2).Info("Could not parse event file (may be incomplete)", "file", filename, "error", err)
		return false
	}

	// Send event to channel with timeout
	timeout := time.NewTimer(10 * time.Second)
	defer timeout.Stop()

	select {
	case r.Events <- event:
		r.logger.V(2).Info("Processed event", "event", event.Event, "uuid", event.UUID)
		return true
	case <-timeout.C:
		r.logger.Info("Timed out writing event to channel")
		return true // Consider it processed to avoid retry loops
	}
}

// Close ensures that appropriate resources are cleaned up
func (r *FileEventReceiver) Close() {
	r.mutex.Lock()
	r.stopped = true
	r.mutex.Unlock()

	r.logger.V(1).Info("File Event API stopped")

	close(r.Events)
}
