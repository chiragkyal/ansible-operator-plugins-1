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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFileEventReceiver(t *testing.T) {
	// Create temporary directory for test
	tempDir, err := os.MkdirTemp("", "fileapi-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Test data
	identifier := "test-123"
	artifactsDir := tempDir
	errChan := make(chan error, 1)

	// Create receiver
	receiver, err := NewFileEventReceiver(identifier, artifactsDir, errChan)
	if err != nil {
		t.Fatalf("Failed to create FileEventReceiver: %v", err)
	}
	defer receiver.Close()

	// Create job_events directory
	jobEventsDir := filepath.Join(artifactsDir, identifier, "job_events")
	if err := os.MkdirAll(jobEventsDir, 0755); err != nil {
		t.Fatalf("Failed to create job_events dir: %v", err)
	}

	// Test events to write
	testEvents := []JobEvent{
		{
			Event:   "playbook_on_start",
			UUID:    "test-uuid-1",
			Counter: 1,
		},
		{
			Event:   "playbook_on_task_start",
			UUID:    "test-uuid-2",
			Counter: 2,
		},
		{
			Event:   "runner_on_ok",
			UUID:    "test-uuid-3",
			Counter: 3,
		},
		{
			Event:   "playbook_on_stats",
			UUID:    "test-uuid-4",
			Counter: 4,
		},
	}

	// Write test event files
	for i, event := range testEvents {
		filename := filepath.Join(jobEventsDir,
			fmt.Sprintf("%d-%s.json", i+1, event.UUID))

		data, err := json.Marshal(event)
		if err != nil {
			t.Fatalf("Failed to marshal event: %v", err)
		}

		if err := os.WriteFile(filename, data, 0644); err != nil {
			t.Fatalf("Failed to write event file: %v", err)
		}
	}

	// Collect events with timeout
	var receivedEvents []JobEvent
	timeout := time.After(5 * time.Second)
	expectedCount := len(testEvents)

	for len(receivedEvents) < expectedCount {
		select {
		case event := <-receiver.Events:
			receivedEvents = append(receivedEvents, event)
			t.Logf("Received event: %s (counter: %d)", event.Event, event.Counter)

		case err := <-errChan:
			t.Fatalf("Received error: %v", err)

		case <-timeout:
			t.Fatalf("Timeout waiting for events. Got %d, expected %d",
				len(receivedEvents), expectedCount)
		}
	}

	// Verify we got all events
	if len(receivedEvents) != expectedCount {
		t.Errorf("Expected %d events, got %d", expectedCount, len(receivedEvents))
	}

	// Verify event ordering (should be by counter/filename)
	for i, event := range receivedEvents {
		expectedCounter := i + 1
		if event.Counter != expectedCounter {
			t.Errorf("Event %d: expected counter %d, got %d",
				i, expectedCounter, event.Counter)
		}
	}

	// Verify specific events were received
	eventTypes := make(map[string]bool)
	for _, event := range receivedEvents {
		eventTypes[event.Event] = true
	}

	expectedTypes := []string{
		"playbook_on_start",
		"playbook_on_task_start",
		"runner_on_ok",
		"playbook_on_stats",
	}

	for _, expectedType := range expectedTypes {
		if !eventTypes[expectedType] {
			t.Errorf("Missing expected event type: %s", expectedType)
		}
	}
}

func TestFileEventReceiver_ExistingFiles(t *testing.T) {
	// Test that existing files are processed when receiver starts
	tempDir, err := os.MkdirTemp("", "fileapi-existing-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	identifier := "existing-test"
	artifactsDir := tempDir
	jobEventsDir := filepath.Join(artifactsDir, identifier, "job_events")

	// Create directory and write event file BEFORE creating receiver
	if err := os.MkdirAll(jobEventsDir, 0755); err != nil {
		t.Fatalf("Failed to create job_events dir: %v", err)
	}

	existingEvent := JobEvent{
		Event:   "existing_event",
		UUID:    "existing-uuid",
		Counter: 1,
	}

	data, err := json.Marshal(existingEvent)
	if err != nil {
		t.Fatalf("Failed to marshal event: %v", err)
	}

	filename := filepath.Join(jobEventsDir, "1-existing-uuid.json")
	if err := os.WriteFile(filename, data, 0644); err != nil {
		t.Fatalf("Failed to write existing event file: %v", err)
	}

	// Now create receiver - it should pick up existing file
	errChan := make(chan error, 1)
	receiver, err := NewFileEventReceiver(identifier, artifactsDir, errChan)
	if err != nil {
		t.Fatalf("Failed to create FileEventReceiver: %v", err)
	}
	defer receiver.Close()

	// Should receive the existing event
	select {
	case event := <-receiver.Events:
		if event.Event != "existing_event" {
			t.Errorf("Expected 'existing_event', got '%s'", event.Event)
		}
		t.Logf("✅ Successfully processed existing file")

	case err := <-errChan:
		t.Fatalf("Received error: %v", err)

	case <-time.After(3 * time.Second):
		t.Fatal("Timeout waiting for existing event")
	}
}

func TestFileEventReceiver_Debug(t *testing.T) {
	// Create temporary directory for test
	tempDir, err := os.MkdirTemp("", "fileapi-debug-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Test data
	identifier := "test-123"
	artifactsDir := tempDir
	errChan := make(chan error, 1)

	t.Logf("Using temp dir: %s", tempDir)
	t.Logf("Identifier: %s", identifier)
	t.Logf("Artifacts dir: %s", artifactsDir)

	// Create receiver
	receiver, err := NewFileEventReceiver(identifier, artifactsDir, errChan)
	if err != nil {
		t.Fatalf("Failed to create FileEventReceiver: %v", err)
	}
	defer receiver.Close()

	// Create job_events directory
	jobEventsDir := filepath.Join(artifactsDir, identifier, "job_events")
	t.Logf("Creating job events dir: %s", jobEventsDir)
	if err := os.MkdirAll(jobEventsDir, 0755); err != nil {
		t.Fatalf("Failed to create job_events dir: %v", err)
	}

	// Check if directory exists
	if _, err := os.Stat(jobEventsDir); os.IsNotExist(err) {
		t.Fatalf("job_events directory was not created: %s", jobEventsDir)
	}

	// Wait a moment for the receiver to start watching
	time.Sleep(100 * time.Millisecond)

	// Create a simple test event
	testEvent := JobEvent{
		Event:   "test_event",
		UUID:    "test-uuid-debug",
		Counter: 1,
	}

	filename := filepath.Join(jobEventsDir, "1-test-uuid-debug.json")
	t.Logf("Writing event file: %s", filename)

	data, err := json.Marshal(testEvent)
	if err != nil {
		t.Fatalf("Failed to marshal event: %v", err)
	}

	if err := os.WriteFile(filename, data, 0644); err != nil {
		t.Fatalf("Failed to write event file: %v", err)
	}

	// Check if file was written
	if _, err := os.Stat(filename); os.IsNotExist(err) {
		t.Fatalf("Event file was not created: %s", filename)
	} else {
		t.Logf("✅ Event file created successfully")
	}

	// Try to receive event with shorter timeout for debugging
	select {
	case event := <-receiver.Events:
		t.Logf("✅ Received event: %s", event.Event)

	case err := <-errChan:
		t.Fatalf("Received error: %v", err)

	case <-time.After(2 * time.Second):
		// Let's check what files are in the directory
		files, readErr := os.ReadDir(jobEventsDir)
		if readErr != nil {
			t.Fatalf("Could not read directory: %v", readErr)
		}

		t.Logf("Files in job_events directory:")
		for _, file := range files {
			t.Logf("  - %s", file.Name())
		}

		t.Fatal("Timeout waiting for event - check debug output above")
	}
}
