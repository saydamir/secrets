package store

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/boltdb/bolt"
)

var bucket = []byte("secrets")

// Bolt implements store.Engine with boltdb
type Bolt struct {
	db *bolt.DB
}

// NewBolt makes persitent boltdb based store
func NewBolt(dbFile string, cleanupDuration time.Duration) (*Bolt, error) {
	log.Printf("[INFO] bolt (persitent) store, %s", dbFile)
	result := Bolt{}
	db, err := bolt.Open(dbFile, 0600, &bolt.Options{Timeout: 1 * time.Second})
	db.Update(func(tx *bolt.Tx) error {
		_, e := tx.CreateBucketIfNotExists(bucket)
		return e
	})
	result.db = db
	result.activateCleaner(cleanupDuration)
	return &result, err
}

// Save with autogenerated ts-uuid as a key. ts prefix for bolt range query
func (s *Bolt) Save(msg *Message) (err error) {

	total := 0
	err = s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucket)
		total = b.Stats().KeyN
		jdata, jerr := json.Marshal(msg)
		if jerr != nil {
			return err
		}
		return b.Put([]byte(msg.Key), jdata)
	})

	log.Printf("[DEBUG] saved, exp=%v, total=%d", msg.Exp, total+1)
	return err
}

// Load by key, removes on first access, checks expire
func (s *Bolt) Load(key string) (result *Message, err error) {

	err = s.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(bucket)
		val := bucket.Get([]byte(key))
		if val == nil {
			log.Printf("[INFO] not found %s", key)
			return ErrLoadRejected
		}
		result = &Message{}
		return json.Unmarshal(val, result)
	})

	return result, err
}

// Remove by key
func (s *Bolt) Remove(key string) (err error) {
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucket).Delete([]byte(key))
	})
}

// IncErr increments error count
func (s *Bolt) IncErr(key string) (count int, err error) {
	msg, err := s.Load(key)
	if err != nil {
		return 0, err
	}
	msg.Errors++
	err = s.Save(msg)
	return msg.Errors, err
}

// activateCleaner runs periodic clenaups to get rid or expired msgs
// detection based on ts (unix time) prefix of the key.
func (s *Bolt) activateCleaner(every time.Duration) {
	log.Printf("[INFO] cleaner activated, every %v", every)

	ticker := time.NewTicker(every)
	go func() {
		for range ticker.C {

			expired := [][]byte{}

			s.db.View(func(tx *bolt.Tx) error {
				c := tx.Bucket(bucket).Cursor()

				// uuid just a place holder to make keys sorted properly by ts prefix
				min := []byte(fmt.Sprintf("%d-06bcb86c-0b6d-4c1b-604a-7a2dbf1ab53b",
					time.Date(2016, 6, 1, 0, 0, 0, 0, time.UTC).Unix()))
				max := []byte(fmt.Sprintf("%d-06bcb86c-0b6d-4c1b-604a-7a2dbf1ab53b", time.Now().Unix()))

				for k, v := c.Seek(min); k != nil && bytes.Compare(k, max) <= 0; k, v = c.Next() {
					expired = append(expired, k)
					_ = v
				}

				return nil
			})

			if len(expired) > 0 {
				log.Printf("[INFO] expired keys=%d", len(expired))
				s.db.Update(func(tx *bolt.Tx) error {
					for _, key := range expired {
						tx.Bucket(bucket).Delete(key)
						if exp, err := strconv.Atoi(strings.Split(string(key), "-")[0]); err == nil {
							log.Printf("[DEBUG] cleaned %s on %v", string(key), time.Unix(int64(exp), 0))
						}
					}
					return nil
				})
			}

		}
	}()
}
