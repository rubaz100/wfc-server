package database

import (
	"encoding/json"
	"wwfc/common"
	"wwfc/logging"
)

const (
	insertEventQuery = `
		INSERT INTO events (event_type, event_data) 
		VALUES ($1, $2) 
		RETURNING id`
)

func (c *Connection) InsertEvent(eventType string, eventData map[string]any) (int, error) {
	data, err := json.Marshal(eventData)
	if err != nil {
		return 0, err
	}

	var eventId int
	err = c.pool.QueryRow(c.ctx, insertEventQuery, eventType, string(data)).Scan(&eventId)
	if err != nil {
		return 0, err
	}
	return eventId, nil
}

func (c *Connection) RegisterEvents(config common.Config, eventTypes []string) {
	if !config.EventReporting.LogToDatabase {
		return
	}
	logging.RegisterEventCallback(eventTypes, func(eventType string, eventData map[string]any) {
		_, err := c.InsertEvent(eventType, eventData)
		if err != nil {
			panic(err)
		}
	})
}
