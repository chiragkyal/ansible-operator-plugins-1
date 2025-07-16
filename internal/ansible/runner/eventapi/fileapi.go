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
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
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

	// watcher for file system events
	watcher *fsnotify.Watcher

	// processedEvents tracks which event files we've already processed
	processedEvents map[string]bool

	// current position in the stdout file
	filePosition int64

	// errChan is a channel for errors
	errChan chan<- error

	// ctx is the context for cancellation
	ctx context.Context
}

// NewFileEventReceiver creates a new file-based event receiver
func NewFileEventReceiver(ident string, artifactPath string, errChan chan<- error) (*FileEventReceiver, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	receiver := &FileEventReceiver{
		Events:          make(chan JobEvent, 1000),
		ArtifactPath:    artifactPath,
		ident:           ident,
		logger:          logf.Log.WithName("fileapi").WithValues("job", ident),
		watcher:         watcher,
		processedEvents: make(map[string]bool),
		filePosition:    0,
		errChan:         errChan,
		ctx:             context.Background(), // Placeholder, will be updated
	}

	// The watcher is not needed for the new job_events directory approach
	// We'll create and watch the proper ansible-runner directory structure

	// Start watching for file changes
	go receiver.watchJobEvents()

	return receiver, nil
}

// watchFiles monitors the artifact directory for new event files
func (r *FileEventReceiver) watchFiles(errChan chan<- error) {
	// Use a ticker to periodically scan for new events as primary mechanism
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case event, ok := <-r.watcher.Events:
			if !ok {
				errChan <- nil
				return
			}

			r.mutex.RLock()
			if r.stopped {
				r.mutex.RUnlock()
				errChan <- nil
				return
			}
			r.mutex.RUnlock()

			// React to file system events
			if event.Op&fsnotify.Write == fsnotify.Write || event.Op&fsnotify.Create == fsnotify.Create {
				r.scanForEvents()
			}

		case <-ticker.C:
			r.mutex.RLock()
			if r.stopped {
				r.mutex.RUnlock()
				errChan <- nil
				return
			}
			r.mutex.RUnlock()

			// Primary mechanism: regularly scan for new events
			r.scanForEvents()

		case err, ok := <-r.watcher.Errors:
			if !ok {
				errChan <- nil
				return
			}
			r.logger.Error(err, "Watcher error")
			// Don't exit on watcher errors, continue with polling
		}
	}
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

// scanForEvents scans both job_events directory and stdout file for new events
func (r *FileEventReceiver) scanForEvents() {
	// Process structured events from job_events directory
	eventsDir := filepath.Join(r.ArtifactPath, "artifacts", r.ident, "job_events")
	r.processJobEventsDir(eventsDir)

	// Process stdout for additional context
	stdoutFile := filepath.Join(r.ArtifactPath, "artifacts", r.ident, "stdout")
	r.processStdoutFile(stdoutFile)
}

// processJobEventsDir scans the job_events directory for new event files
func (r *FileEventReceiver) processJobEventsDir(eventsDir string) {
	entries, err := os.ReadDir(eventsDir)
	if err != nil {
		if !os.IsNotExist(err) {
			r.logger.V(2).Info("job_events directory not yet created", "dir", eventsDir)
		}
		return
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		// Skip if we've already processed this event file
		eventFile := filepath.Join(eventsDir, entry.Name())
		if r.processedEvents[eventFile] {
			continue
		}

		if r.processEventFile(eventFile) {
			r.processedEvents[eventFile] = true
		}
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

// processStdoutFile processes stdout output for progress tracking
func (r *FileEventReceiver) processStdoutFile(filename string) {
	file, err := os.Open(filename)
	if err != nil {
		return // Stdout file may not exist yet
	}
	defer file.Close()

	// Get file size
	stat, err := file.Stat()
	if err != nil {
		return
	}

	// If file is smaller than our position, reset (log was rotated/recreated)
	if stat.Size() < r.filePosition {
		r.filePosition = 0
	}

	// Seek to last known position
	if _, err := file.Seek(r.filePosition, 0); err != nil {
		return
	}

	reader := bufio.NewReader(file)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				// Update position and wait for more content
				if pos, err := file.Seek(0, 1); err == nil {
					r.filePosition = pos
				}
			}
			break
		}

		r.filePosition += int64(len(line))

		// Extract task information from stdout for additional context
		r.processStdoutLine(strings.TrimSpace(line))
	}
}

// processStdoutLine extracts useful information from stdout lines
func (r *FileEventReceiver) processStdoutLine(line string) {
	// Look for task start patterns in stdout
	if strings.Contains(line, "TASK [") && strings.Contains(line, "]") {
		r.logger.V(1).Info("Task progress", "line", line)
	} else if strings.Contains(line, "PLAY [") {
		r.logger.V(1).Info("Playbook progress", "line", line)
	} else if strings.Contains(line, "PLAY RECAP") {
		r.logger.V(1).Info("Playbook completed", "line", line)
	}
}

// Close ensures that appropriate resources are cleaned up
func (r *FileEventReceiver) Close() {
	r.mutex.Lock()
	r.stopped = true
	r.mutex.Unlock()

	r.logger.V(1).Info("File Event API stopped")

	if r.watcher != nil {
		r.watcher.Close()
	}

	close(r.Events)
}
