package main

import (
	"context"
	"github.com/cloudflare/cloudflare-go"
	"log"
	"time"
)

const CLOUDFLARE_TOKEN = "" //os.Getenv("CLOUDFLARE_API_TOKEN")
const ZONE_ID = ""

func updateRecord(pubkey string, ip string, port string) {
	api, err := cloudflare.NewWithAPIToken(CLOUDFLARE_TOKEN)

	if err != nil {
		log.Fatal(err)
	}

	// Most API calls require a Context
	ctx := context.Background()

	api.CreateDNSRecord(ctx, ZONE_ID, cloudflare.DNSRecord{
		CreatedOn:  time.Time{},
		ModifiedOn: time.Time{},
		Type:       "A",
		Name:       pubkey,
		Content:    "",
		Meta:       nil,
		Data:       nil,
		ID:         "",
		ZoneID:     "",
		ZoneName:   "",
		Priority:   nil,
		TTL:        0,
		Proxied:    nil,
		Proxiable:  false,
		Locked:     false,
	})

	api.CreateDNSRecord(ctx, ZONE_ID, cloudflare.DNSRecord{
		CreatedOn:  time.Time{},
		ModifiedOn: time.Time{},
		Type:       "SRV",
		Name:       "_wireguard._udp." + pubkey + ".wireguard",
		Content:    "",
		Meta:       nil,
		Data:       nil,
		ID:         "",
		ZoneID:     "",
		ZoneName:   "",
		Priority:   nil,
		TTL:        0,
		Proxied:    nil,
		Proxiable:  false,
		Locked:     false,
	})
}
