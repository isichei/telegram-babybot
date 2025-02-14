package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

const (
	EventFeed      EventType = "feed"
	EventFeedL     EventType = "feed L"
	EventFeedR     EventType = "feed R"
	EventNappyPoo  EventType = "nappy poo"
	EventNappyWee  EventType = "nappy wee"
	EventNappyBoth EventType = "nappy poo and wee"
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

var (
	bot         *tgbotapi.BotAPI
	err         error
	events      []Event
	eventsMutex sync.Mutex
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

func getEventsFromLastTwoFiles() []Event {
	prevEvents, err := createEventsFromFile(false)
	if err != nil {
		log.Panicf("Error creating events from file for day before. %s", err)
	}
	todayEvents, err := createEventsFromFile(true)
	if err != nil {
		log.Panicf("Error creating events from file for today. %s", err)
	}
	return append(prevEvents, todayEvents...)
}

// Save the events store to simple txt file
func saveEventsToFile() {
	eventsMutex.Lock()
	defer eventsMutex.Unlock()

	now := time.Now()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	fileName := fmt.Sprintf("data/event_%d_%d_%d.txt", now.Year(), now.Month(), now.Day())
	file, err := os.Create(fileName)
	if err != nil {
		log.Println("Error creating file:", err)
		return
	}
	defer file.Close()

	for _, event := range events {
		if event.Time.After(midnight) || event.Time.Equal(midnight) {
			file.WriteString(eventToString(event, true))
			file.WriteString("\n")
		}
	}
}

// addEvent adds a new event and saves to file
func addEvent(msg EventType, t time.Time) {
	eventsMutex.Lock()
	newEvent := Event{ID: len(events) + 1, Event: msg, Time: t}
	events = append(events, newEvent)
	eventsMutex.Unlock()

	saveEventsToFile()
}

func main() {

	bot, err = tgbotapi.NewBotAPI(os.Getenv("TELEGRAM_API_KEY"))
	if err != nil {
		// Abort if something is wrong
		log.Panicf("Error creating bot. %s", err)
	}
	events = getEventsFromLastTwoFiles()

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
	go receiveUpdates(ctx, &wg, updates)
	wg.Wait()

	log.Println("Bot shutdown complete")
}

// Function to handle context and process Telegram messages
func receiveUpdates(ctx context.Context, wg *sync.WaitGroup, updates tgbotapi.UpdatesChannel) {
	defer wg.Done()
	for {
		select {
		// stop looping if ctx is cancelled
		case <-ctx.Done():
			log.Println("Message update receiver got completed context")
			return
		// receive update from channel and then handle it
		case update := <-updates:
			handleUpdate(update)
		}
	}
}

func handleUpdate(update tgbotapi.Update) {
	switch {
	// Handle messages
	case update.Message != nil:
		handleMessage(update.Message)
		break

	// Handle button clicks
	case update.CallbackQuery != nil:
		handleButton(update.CallbackQuery)
		break
	}
}

func handleMessage(message *tgbotapi.Message) {
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
		),
	}

	// If word feed is sent then send a feeding menu choice
	eventClassification := parseEventTypeFromUserMsg(text)
	if eventClassification != "" {
		handleFeedOrNappyEvent(eventClassification, messageTime, message.Chat.ID)
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

func parseEventTypeFromUserMsg(msg string) EventType {
	// is this a feed message
	if strings.HasPrefix(msg, "feed") {
		if strings.Contains(msg, " r") {
			return "feed R"
		} else if strings.Contains(msg, " l") {
			return "feed L"
		} else {
			return "feed"
		}
	}
	if strings.HasPrefix(msg, "nappy") {
		if (strings.Contains(msg, "wee") && strings.Contains(msg, "poo")) || strings.Contains(msg, "wp") {
			return "nappy poo and wee"
		} else if strings.Contains(msg, "wee") {
			return "nappy wee"
		} else if strings.Contains(msg, "poo") {
			return "nappy poo"
		} else {
			return "nappy poo and wee"
		}
	}
	return ""
}

func handleFeedOrNappyEvent(eventClassication EventType, messageTime time.Time, chatId int64) {
	addEvent(eventClassication, messageTime)
	replyText := fmt.Sprintf("%s recorded at %s. ", eventClassication, messageTime.Format(time.Kitchen))

	// Create a message to echo back
	msg := tgbotapi.NewMessage(chatId, replyText)

	// Send the message
	if _, err := bot.Send(msg); err != nil {
		log.Println("Error sending message:", err)
	}
}

func getLastEvent(filterBy string) Event {
	if len(events) == 0 {
		return Event{}
	}
	lastEvent := events[0]
	for _, event := range events {
		if event.Time.After(lastEvent.Time) && strings.HasPrefix(string(event.Event), filterBy) {
			lastEvent = event
		}
	}
	return lastEvent
}

func getLastEventResp(filterBy string) string {
	event := getLastEvent(filterBy)
	return eventToString(event, false)
}

func getLastEvents(filterBy string) []Event {
	dayBefore := time.Now().Add(-24 * time.Hour)
	filteredEvents := []Event{}
	if len(events) == 0 {
		return filteredEvents
	}

	for _, event := range events {
		if event.Time.After(dayBefore) && strings.HasPrefix(string(event.Event), filterBy) {
			filteredEvents = append(filteredEvents, event)
		}
	}
	return filteredEvents
}

func getLastEventsResp(filterBy string) string {
	events24 := getLastEvents(filterBy)
	botResponse := ""
	for _, event := range events24 {
		botResponse += fmt.Sprintf("%s    (%d)\n", eventToString(event, false), event.ID)
	}
	return botResponse
}

func handleButton(query *tgbotapi.CallbackQuery) {
	message := query.Message
	var botResponse string

	switch query.Data {
	case "nappy":
		botResponse = getLastEventResp("nappy")
		log.Println("Sending last nappy")
	case "feed":
		botResponse = getLastEventResp("feed")
		log.Println("Sending last feed")
	case "nappy24":
		botResponse = getLastEventsResp("nappy")
		log.Println("Sending the last nappy events over the last 24h")
	case "feed24":
		botResponse = getLastEventsResp("feed")
		log.Println("Sending the last feed events over the last 24h")
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
