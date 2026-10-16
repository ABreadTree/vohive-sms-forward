package store

import (
	"database/sql"
	"errors"
)

// PrepareNotificationTargets atomically retires pending work for changed
// destinations before a new sender is allowed to run.
func (s *Store) PrepareNotificationTargets(barkFP, telegramFP string) error {
	if barkFP == "" || telegramFP == "" {
		return errors.New("notification target fingerprint is empty")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, target := range []struct{ channel, fp string }{{"bark", barkFP}, {"telegram", telegramFP}} {
		if err := prepareNotificationTarget(tx, target.channel, target.fp); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// PrepareNotificationTarget rotates only one destination, leaving the other
// channel's pending deliveries and Telegram state untouched.
func (s *Store) PrepareNotificationTarget(channel, fp string) error {
	if (channel != "bark" && channel != "telegram") || fp == "" {
		return errors.New("invalid notification target")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := prepareNotificationTarget(tx, channel, fp); err != nil {
		return err
	}
	return tx.Commit()
}

func prepareNotificationTarget(tx *sql.Tx, channel, fp string) error {
	key := "notification_" + channel + "_target"
	{
		var old string
		err := tx.QueryRow(`SELECT value FROM meta WHERE key=?`, key).Scan(&old)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil && old == fp {
			return nil
		}
		if _, err := tx.Exec(`DELETE FROM notification_deliveries WHERE channel=? AND state='pending'`, channel); err != nil {
			return err
		}
		if channel == "telegram" {
			for _, query := range []string{`DELETE FROM telegram_drafts`, `DELETE FROM telegram_commands`, `DELETE FROM meta WHERE key='telegram_last_update'`} {
				if _, err := tx.Exec(query); err != nil {
					return err
				}
			}
		}
		if _, err := tx.Exec(`INSERT INTO meta(key,value) VALUES (?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, fp); err != nil {
			return err
		}
	}
	return nil
}
