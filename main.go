package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

const (
	eventsBufferSize           = 100
	EventFeed        EventType = "feed"
	EventFeedL       EventType = "feed L"
	EventFeedR       EventType = "feed R"
	EventNappyPoo    EventType = "nappy poo"
	EventNappyWee    EventType = "nappy wee"
	EventNappyBoth   EventType = "nappy poo and wee"
	EventSleepStart  EventType = "sleep start"
	EventSleepEnd    EventType = "sleep end"
	EventUnknown     EventType = ""
)

type EventType string

type Menu struct {
	Text   string
	Markup tgbotapi.InlineKeyboardMarkup
}

type Event struct {
	ID    int
	Event EventType
	Time  time.Time
}

type EventStore struct {
	events      []Event
	eventsMutex sync.Mutex
	writeToFile bool // Can't be arsed to do any interfaces. This works for simple testing
}

// Save the events store to simple txt file
func (es *EventStore) saveEventsToFile() {
	es.eventsMutex.Lock()
	defer es.eventsMutex.Unlock()

	now := time.Now()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	fileName := fmt.Sprintf("data/event_%d_%d_%d.txt", now.Year(), now.Month(), now.Day())
	file, err := os.Create(fileName)
	if err != nil {
		log.Println("Error creating file:", err)
		return
	}
	defer file.Close()

	for _, event := range es.events {
		if event.Time.After(midnight) || event.Time.Equal(midnight) {
			file.WriteString(eventToString(event, true))
			file.WriteString("\n")
		}
	}
}

// addEvent adds a new event and saves to file
func (es *EventStore) addEvent(msg EventType, t time.Time) {
	es.eventsMutex.Lock()
	newEvent := Event{ID: len(es.events) + 1, Event: msg, Time: t}

	if len(es.events) >= eventsBufferSize {
		es.events = es.events[1:]
	}
	es.events = append(es.events, newEvent)
	es.eventsMutex.Unlock()

	if es.writeToFile {
		es.saveEventsToFile()
	}
}

var (
	bot            *tgbotapi.BotAPI
	err            error
	allNappyEvents = []EventType{EventNappyPoo, EventNappyWee, EventNappyBoth}
	allFeedEvents  = []EventType{EventFeed, EventFeedR, EventFeedL}
	allSleepEvents = []EventType{EventSleepStart, EventSleepEnd}
)

func eventToString(event Event, system bool) string {
	if system {
		return fmt.Sprintf("%s %s", event.Time.Format(time.RFC3339), event.Event)
	} else {
		return fmt.Sprintf("%s %s", event.Time.Format(time.Kitchen), event.Event)
	}
}

func stringToEvent(s string, id int) Event {
	parts := strings.SplitN(s, " ", 2)
	loc, _ := time.LoadLocation("Europe/London")
	eventTime, err := time.ParseInLocation(time.RFC3339, parts[0], loc)
	if err != nil {
		panic(fmt.Sprintf("Failed to get timezone. %s", err))
	}
	return Event{id, EventType(parts[1]), eventTime}
}

// Create events from txt file log of each days events
func createEventsFromFile(currentDay bool) ([]Event, error) {
	var d time.Time
	if currentDay {
		d = time.Now()
	} else {
		d = time.Now().Add(-24 * time.Hour)
	}
	fileName := fmt.Sprintf("data/event_%d_%d_%d.txt", d.Year(), d.Month(), d.Day())
	file, err := os.Open(fileName)

	// Try to open the file
	if err != nil {
		// If the file doesn't exist, return empty slice
		if os.IsNotExist(err) {
			return []Event{}, nil
		}
		// Return any other errors
		return nil, err
	}
	defer file.Close()

	// Create a scanner to read the file line by line
	scanner := bufio.NewScanner(file)
	events := []Event{}

	// Read each line and append to our slice
	counter := 0
	for scanner.Scan() {
		events = append(events, stringToEvent(scanner.Text(), counter))
		counter++
	}

	// Check for any errors that occurred during scanning
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return events, nil
}

func getEventsFromLastTwoFiles() EventStore {
	prevEvents, err := createEventsFromFile(false)
	if err != nil {
		log.Panicf("Error creating events from file for day before. %s", err)
	}
	todayEvents, err := createEventsFromFile(true)
	if err != nil {
		log.Panicf("Error creating events from file for today. %s", err)
	}
	events := append(prevEvents, todayEvents...)
	return EventStore{events, sync.Mutex{}, true}
	// return append(prevEvents, todayEvents...)
}

func main() {

	bot, err = tgbotapi.NewBotAPI(os.Getenv("TELEGRAM_API_KEY"))
	if err != nil {
		// Abort if something is wrong
		log.Panicf("Error creating bot. %s", err)
	}
	events := getEventsFromLastTwoFiles()

	// Set this to true to log all interactions with telegram servers
	bot.Debug = false

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	// Create context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// listen to system signals (e.g., Ctrl+C) to kill bot
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		log.Println("Received termination signal, shutting down...")
		cancel()
	}()

	// `updates` is a golang channel which receives telegram updates
	updates := bot.GetUpdatesChan(u)

	// Run the recieve go routine and wait
	var wg sync.WaitGroup
	wg.Add(1)
	go receiveUpdates(ctx, &wg, updates, &events)
	wg.Wait()

	log.Println("Bot shutdown complete")
}

// Function to handle context and process Telegram messages
func receiveUpdates(ctx context.Context, wg *sync.WaitGroup, updates tgbotapi.UpdatesChannel, events *EventStore) {
	defer wg.Done()
	for {
		select {
		// stop looping if ctx is cancelled
		case <-ctx.Done():
			log.Println("Message update receiver got completed context")
			return
		// receive update from channel and then handle it
		case update := <-updates:
			handleUpdate(update, events)
		}
	}
}

func handleUpdate(update tgbotapi.Update, eStore *EventStore) {
	switch {
	// Handle messages
	case update.Message != nil:
		handleMessage(update.Message, eStore)
		break

	// Handle button clicks
	case update.CallbackQuery != nil:
		handleButton(update.CallbackQuery, eStore)
		break
	}
}

func handleMessage(message *tgbotapi.Message, events *EventStore) {
	user := message.From
	text := strings.ToLower(strings.Trim(message.Text, " "))
	loc, _ := time.LoadLocation("Europe/London")
	messageTime := time.Unix(int64(message.Date), 0).In(loc)

	if user == nil {
		return
	}

	log.Printf("%s wrote %s at %s", user.FirstName, text, messageTime.Format("15:04:05"))

	var err error

	statsMenu := Menu{
		Text: "<b>What stats do you want?</b>\n\n",
		Markup: tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("Last nappy change?", "nappy"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("Last feed?", "feed"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("Last 24 hours of nappy changes?", "nappy24"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("Last 24 hours of feeds?", "feed24"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("Last 24 hours of sleeps?", "sleep24"),
			),
		),
	}

	eventClassification := parseEventTypeFromUserMsg(text)

	// Parse time from message if it contains @ HH:MM format
	messageTimeOverride := parseTimeFromMessage(text)
	if messageTimeOverride != nil {
		messageTime = *messageTimeOverride
	}

	if contains(allSleepEvents, eventClassification) {
		reply := handleSleep(eventClassification, messageTime, events)
		replyWithText(reply, message.Chat.ID)
	} else if eventClassification != EventUnknown {
		reply := handleFeedOrNappyEvent(eventClassification, messageTime, events)
		replyWithText(reply, message.Chat.ID)
	} else if strings.HasPrefix(text, "stats") {
		err = sendMenu(message.Chat.ID, statsMenu)
	} else {
		// Send a message to say you are alive but no action required
		msg := tgbotapi.NewMessage(message.Chat.ID, "...")

		// Send the message
		if _, err := bot.Send(msg); err != nil {
			log.Println("Error sending no response msg:", err)
		}
	}

	if err != nil {
		log.Printf("An error occured: %s", err.Error())
	}
}

func parseTimeFromMessage(text string) *time.Time {
	if !strings.Contains(text, " @ ") {
		return nil
	}

	// Extract time part after @
	parts := strings.Split(text, " @ ")
	if len(parts) != 2 {
		return nil
	}

	// Try to parse time in 24h format
	timeStr := strings.TrimSpace(parts[1])
	t, err := time.Parse("15:04", timeStr)
	if err != nil {
		return nil
	}

	// Get current time in London timezone
	loc, _ := time.LoadLocation("Europe/London")
	now := time.Now().In(loc)

	// Construct full datetime using today's date
	result := time.Date(
		now.Year(), now.Month(), now.Day(),
		t.Hour(), t.Minute(), 0, 0,
		loc,
	)

	// If parsed time is in future, assume it was from yesterday
	if result.After(now) {
		result = result.AddDate(0, 0, -1)
	}

	return &result
}

func parseEventTypeFromUserMsg(msg string) EventType {
	// is this a feed message
	if strings.HasPrefix(msg, "feed") {
		if strings.Contains(msg, " r") {
			return EventFeedR
		} else if strings.Contains(msg, " l") {
			return EventFeedL
		} else {
			return EventFeed
		}
	}
	if strings.HasPrefix(msg, "nappy") {
		if (strings.Contains(msg, "wee") && strings.Contains(msg, "poo")) || strings.Contains(msg, "wp") {
			return EventNappyBoth
		} else if strings.Contains(msg, "wee") {
			return EventNappyWee
		} else if strings.Contains(msg, "poo") {
			return EventNappyPoo
		} else {
			return EventNappyBoth
		}
	}
	if strings.HasPrefix(msg, "sleep") && strings.Contains(msg, "end") {
		return EventSleepEnd
	}
	if strings.HasPrefix(msg, "sleep") {
		return EventSleepStart
	}
	return EventUnknown
}

func replyWithText(reply string, chatId int64) {
	// Create a message to echo back
	msg := tgbotapi.NewMessage(chatId, reply)

	// Send the message
	if _, err := bot.Send(msg); err != nil {
		log.Println("Error sending message:", err)
	}
}

func handleSleep(eventClassification EventType, eventTime time.Time, eStore *EventStore) string {
	var replyText string

	relevantSleepEvents := getLastEventsBefore(eStore.events, allSleepEvents, eventTime)
	lastEventType := EventUnknown
	if len(relevantSleepEvents) > 0 {
		lastEventType = relevantSleepEvents[len(relevantSleepEvents)-1].Event
	}

	if eventClassification == EventSleepStart && lastEventType == EventSleepStart {
		replyText = fmt.Sprintf("Last recorded sleep start @ %s was not ended. Send a 'sleep end @ HH:SS' to end it before starting a new sleep.", eventTime.Format(time.Kitchen))
	} else if eventClassification == EventSleepEnd && lastEventType == EventSleepEnd {
		replyText = "No sleep start was recorded so cannot end. Send a 'sleep start @ HH:SS' first before ending it."
	} else {
		replyText = fmt.Sprintf("%s recorded at %s. ", eventClassification, eventTime.Format(time.Kitchen))
		eStore.addEvent(eventClassification, eventTime)
	}
	return replyText
}

func handleFeedOrNappyEvent(eventClassification EventType, messageTime time.Time, eStore *EventStore) string {
	eStore.addEvent(eventClassification, messageTime)
	replyText := fmt.Sprintf("%s recorded at %s. ", eventClassification, messageTime.Format(time.Kitchen))
	return replyText

}

func getLastEventResp(events []Event, filterBy []EventType) string {
	event := getLastEvent(events, filterBy)
	return eventToString(event, false)
}

func getLastEvent(events []Event, filterBy []EventType) Event {
	if len(events) == 0 {
		return Event{}
	}
	lastEvent := events[0]
	for _, event := range events {
		if event.Time.After(lastEvent.Time) && contains(filterBy, event.Event) {
			lastEvent = event
		}
	}
	return lastEvent
}

func getLastEventsAfter(events []Event, filterBy []EventType, since time.Time) []Event {

	filteredEvents := []Event{}
	if len(events) == 0 {
		return filteredEvents
	}

	for _, event := range events {
		if event.Time.After(since) && contains(filterBy, event.Event) {
			filteredEvents = append(filteredEvents, event)
		}
	}

	sort.Slice(filteredEvents, func(i, j int) bool {
		return filteredEvents[i].Time.Before(filteredEvents[j].Time)
	})
	return filteredEvents
}

func getLastEventsBefore(events []Event, filterBy []EventType, until time.Time) []Event {

	filteredEvents := []Event{}
	if len(events) == 0 {
		return filteredEvents
	}

	for _, event := range events {
		if event.Time.Before(until) && contains(filterBy, event.Event) {
			filteredEvents = append(filteredEvents, event)
		}
	}

	sort.Slice(filteredEvents, func(i, j int) bool {
		return filteredEvents[i].Time.Before(filteredEvents[j].Time)
	})
	return filteredEvents
}

func getLastEventsResp(events []Event, filterBy []EventType, sinse time.Time) string {
	events24 := getLastEventsAfter(events, filterBy, sinse)
	botResponse := ""
	for _, event := range events24 {
		botResponse += fmt.Sprintf("%s\n", eventToString(event, false))
	}
	return botResponse
}

func handleButton(query *tgbotapi.CallbackQuery, eStore *EventStore) {
	message := query.Message
	var botResponse string

	switch query.Data {
	case "nappy":
		botResponse = getLastEventResp(eStore.events, allNappyEvents)
		log.Println("Sending last nappy")
	case "feed":
		botResponse = getLastEventResp(eStore.events, allFeedEvents)
		log.Println("Sending last feed")
	case "nappy24":
		botResponse = getLastEventsResp(eStore.events, allNappyEvents, time.Now().Add(-24*time.Hour))
		log.Println("Sending the last nappy events over the last 24h")
	case "feed24":
		botResponse = getLastEventsResp(eStore.events, allFeedEvents, time.Now().Add(-24*time.Hour))
		log.Println("Sending the last feed events over the last 24h")
	case "sleepToday":
		now := time.Now()
		midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		botResponse = getLastEventsResp(eStore.events, allSleepEvents, midnight)
		log.Println("Sending the last sleep events for today")
	default:
		log.Println("You shouldn't be here")
	}

	callbackCfg := tgbotapi.NewCallback(query.ID, "")
	bot.Send(callbackCfg)

	// Replace menu text and keyboard
	msg := tgbotapi.NewMessage(message.Chat.ID, botResponse)

	bot.Send(msg)
}

func sendMenu(chatId int64, menu Menu) error {
	msg := tgbotapi.NewMessage(chatId, menu.Text)
	msg.ParseMode = tgbotapi.ModeHTML
	msg.ReplyMarkup = menu.Markup
	_, err := bot.Send(msg)
	return err
}

func contains(events []EventType, event EventType) bool {
	for _, e := range events {
		if e == event {
			return true
		}
	}
	return false
}
