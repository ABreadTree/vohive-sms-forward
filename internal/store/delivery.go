package store

import (
	"database/sql"
	"strings"
	"time"
)

type Delivery struct {
	MessageID int64
	Channel   string
	Attempts  int
	NextAt    time.Time
}

func (s *Store) DueDeliveries(now time.Time, limit int) ([]Delivery, error) {
	rows, err := s.db.Query(`SELECT message_id, channel, attempts, next_at
	    FROM notification_deliveries WHERE state='pending' AND next_at <= ?
	    ORDER BY next_at, message_id, channel LIMIT ?`, now.Unix(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Delivery
	for rows.Next() {
		var d Delivery
		var next int64
		if err := rows.Scan(&d.MessageID, &d.Channel, &d.Attempts, &next); err != nil {
			return nil, err
		}
		d.NextAt = time.Unix(next, 0)
		out = append(out, d)
	}
	return out, rows.Err()
}

// DueDeliveriesByChannel prevents a slow platform from occupying another
// platform's bounded batch and gives each worker independent forward progress.
func (s *Store) DueDeliveriesByChannel(channel string, now time.Time, limit int) ([]Delivery, error) {
	rows, err := s.db.Query(`SELECT message_id, channel, attempts, next_at
	    FROM notification_deliveries WHERE state='pending' AND channel=? AND next_at <= ?
	    ORDER BY next_at, message_id LIMIT ?`, channel, now.Unix(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Delivery
	for rows.Next() {
		var d Delivery
		var next int64
		if err := rows.Scan(&d.MessageID, &d.Channel, &d.Attempts, &next); err != nil {
			return nil, err
		}
		d.NextAt = time.Unix(next, 0)
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) GetMessage(id int64) (Message, error) {
	var m Message
	err := s.db.QueryRow(`SELECT id, sim_id, direction, peer, body, ts
	    FROM messages WHERE id=?`, id).Scan(&m.ID, &m.SimID, &m.Direction,
		&m.Peer, &m.Body, &m.Timestamp)
	return m, err
}

func (s *Store) MarkDeliverySuccess(messageID int64, channel string) error {
	res, err := s.db.Exec(`UPDATE notification_deliveries SET state='done', last_error=''
	    WHERE message_id=? AND channel=? AND state='pending'`, messageID, channel)
	if err != nil {
		return err
	}
	return requireOneDelivery(res)
}

func (s *Store) MarkDeliveryRetry(messageID int64, channel string, next time.Time, summary string) error {
	// Callers supply a safe error category, never a raw HTTP body or URL.
	summary = strings.ReplaceAll(strings.ReplaceAll(summary, "\n", " "), "\r", " ")
	if len(summary) > 200 {
		summary = summary[:200]
	}
	res, err := s.db.Exec(`UPDATE notification_deliveries
	    SET attempts=attempts+1, next_at=?, last_error=?
	    WHERE message_id=? AND channel=? AND state='pending'`,
		next.Unix(), summary, messageID, channel)
	if err != nil {
		return err
	}
	return requireOneDelivery(res)
}

func requireOneDelivery(res sql.Result) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return sql.ErrNoRows
	}
	return nil
}
