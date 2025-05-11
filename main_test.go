package main

import (
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	LAYOUT                 = "2006-01-02 15:04"
	failedStartEventPrefix = "Last recorded sleep start"
	failedEndEventPrefix   = "No sleep start"
)

func TestHandleSleepHappyPaths(t *testing.T) {
	eStore := EventStore{events: []Event{}, eventsMutex: sync.Mutex{}, writeToFile: false}
	reply := handleSleep(EventSleepStart, time.Now(), &eStore)
	if !strings.HasPrefix(reply, string(EventSleepStart)) {
		t.Errorf("Expected sleepEvent (%s) to be accepted but got this response: %s", string(EventSleepStart), reply)
	}
	reply = handleSleep(EventSleepEnd, time.Now(), &eStore)
	if !strings.HasPrefix(reply, string(EventSleepEnd)) {
		t.Errorf("Expected sleepEvent (%s) to be accepted but got this response: %s", string(EventSleepEnd), reply)
	}
}

func TestHandleSleepSimpleUnhappyPaths(t *testing.T) {
	eStore := EventStore{events: []Event{}, eventsMutex: sync.Mutex{}, writeToFile: false}
	firstTime, err := time.Parse(LAYOUT, "2025-01-01 15:04")
	if err != nil {
		t.Fatal("Error parsing time in test setup:", err)
	}

	// Add sleep start
	eStore.addEvent(EventSleepStart, firstTime)
	secondTime := firstTime.Add(time.Hour)

	// Add another sleep start an hour later (should fail)
	reply := handleSleep(EventSleepStart, secondTime, &eStore)
	if !strings.HasPrefix(reply, failedStartEventPrefix) {
		t.Errorf("Expected sleepEvent (%s) to fail but got this response: %s", string(EventSleepStart), reply)
	}

	// End the sleep start (should succeed)
	reply = handleSleep(EventSleepEnd, secondTime, &eStore)
	if !strings.HasPrefix(reply, string(EventSleepEnd)) {
		t.Errorf("Expected sleepEvent (%s) to fail but got this response: %s", string(EventSleepStart), reply)
	}

	// Add a sleep end an hour after (should fail)
	thirdTime := secondTime.Add(time.Hour)
	reply = handleSleep(EventSleepEnd, thirdTime, &eStore)
	if !strings.HasPrefix(reply, failedEndEventPrefix) {
		t.Errorf("Expected sleepEvent (%s) to fail but got this response: %s", string(EventSleepStart), reply)
	}
}

func TestHandleSleepComplexScenarios(t *testing.T) {

	eStore := EventStore{events: []Event{}, eventsMutex: sync.Mutex{}, writeToFile: false}

	// Create base times for our test: 2pm and 4pm
	baseTime, err := time.Parse(LAYOUT, "2025-01-01 14:00")
	if err != nil {
		t.Fatal("Error parsing time in test setup:", err)
	}

	secondTime := baseTime.Add(2 * time.Hour)

	// Add initial sleep start and sleep end
	eStore.addEvent(EventSleepStart, baseTime)
	eStore.addEvent(EventSleepEnd, secondTime)

	// Test scenario 1: Add sleep start before existing events (1pm)
	beforeTime := baseTime.Add(-1 * time.Hour)
	reply := handleSleep(EventSleepStart, beforeTime, &eStore)
	if !strings.HasPrefix(reply, string(EventSleepStart)) {
		t.Errorf("Expected sleep start at %s to be accepted but got: %s",
			beforeTime.Format(time.Kitchen), reply)
	}

	// Test scenario 2: Add sleep start between existing events (3pm)
	// This should be rejected because we already have a sleep start without an end
	betweenTime := baseTime.Add(time.Hour) // 3pm
	reply = handleSleep(EventSleepStart, betweenTime, &eStore)
	if !strings.HasPrefix(reply, failedStartEventPrefix) {
		t.Errorf("Expected sleep start at %s to be rejected but got: %s",
			betweenTime.Format(time.Kitchen), reply)
	}

	// Test scenario 3: Add sleep start after existing events (5pm)
	afterTime := secondTime.Add(time.Hour) // 5pm
	reply = handleSleep(EventSleepStart, afterTime, &eStore)
	if !strings.HasPrefix(reply, string(EventSleepStart)) {
		t.Errorf("Expected sleep start at %s to be accepted but got: %s",
			afterTime.Format(time.Kitchen), reply)
	}

	// Test scenario 4: Add sleep start at exact same time as existing sleep start (2pm)
	reply = handleSleep(EventSleepStart, baseTime, &eStore)
	if strings.HasPrefix(reply, string(EventSleepStart)) {
		t.Errorf("Expected sleep start at %s to be rejected but it was accepted: %s",
			baseTime.Format(time.Kitchen), reply)
	}

	// Create a new event store for sleep end testing
	eStoreEnd := EventStore{events: []Event{}, eventsMutex: sync.Mutex{}, writeToFile: false}
	eStoreEnd.addEvent(EventSleepStart, baseTime) // 2pm: Sleep start
	eStoreEnd.addEvent(EventSleepEnd, secondTime) // 4pm: Sleep end

	// Test scenario 5: Add sleep end before existing sleep start (1pm)
	// With our implementation, this is checking for sleep starts before 1pm, which there are none,
	// so it will be accepted
	reply = handleSleep(EventSleepEnd, beforeTime, &eStoreEnd)
	if !strings.HasPrefix(reply, string(EventSleepEnd)) {
		t.Errorf("Expected sleep end at %s to be accepted but got: %s",
			beforeTime.Format(time.Kitchen), reply)
	}

	// Test scenario 6: Add sleep end between existing sleep start and end (3pm)
	reply = handleSleep(EventSleepEnd, betweenTime, &eStoreEnd)
	if !strings.HasPrefix(reply, string(EventSleepEnd)) {
		t.Errorf("Expected sleep end at %s to be accepted but got: %s",
			betweenTime.Format(time.Kitchen), reply)
	}

	// Test scenario 7: Add sleep end at exact same time as existing sleep end (4pm)
	reply = handleSleep(EventSleepEnd, secondTime, &eStoreEnd)
	if !strings.HasPrefix(reply, "No sleep start") {
		t.Errorf("Expected sleep end at %s to be rejected but got: %s",
			secondTime.Format(time.Kitchen), reply)
	}

	// Test scenario 8: Add sleep end after everything else (5pm)
	reply = handleSleep(EventSleepEnd, afterTime, &eStoreEnd)
	if !strings.HasPrefix(reply, "No sleep start") {
		t.Errorf("Expected sleep end at %s to be rejected but got: %s",
			afterTime.Format(time.Kitchen), reply)
	}

	// Test multiple sleep cycles
	eStoreCycles := EventStore{events: []Event{}, eventsMutex: sync.Mutex{}, writeToFile: false}

	// First sleep cycle
	t1 := baseTime
	t2 := baseTime.Add(time.Hour)

	// Second sleep cycle
	t3 := baseTime.Add(3 * time.Hour)
	t4 := baseTime.Add(5 * time.Hour)

	// Add first sleep cycle
	eStoreCycles.addEvent(EventSleepStart, t1)
	eStoreCycles.addEvent(EventSleepEnd, t2)

	// Test adding second sleep cycle
	reply = handleSleep(EventSleepStart, t3, &eStoreCycles)
	if !strings.HasPrefix(reply, string(EventSleepStart)) {
		t.Errorf("Expected second sleep start at %s to be accepted but got: %s",
			t3.Format(time.Kitchen), reply)
	}

	reply = handleSleep(EventSleepEnd, t4, &eStoreCycles)
	if !strings.HasPrefix(reply, string(EventSleepEnd)) {
		t.Errorf("Expected second sleep end at %s to be accepted but got: %s",
			t4.Format(time.Kitchen), reply)
	}
}
