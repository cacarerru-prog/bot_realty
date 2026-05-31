package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"flatradar/internal/model"
)

// Kufar — коллектор объявлений о продаже квартир в Минске
// через публичный JSON API api.kufar.by.
type Kufar struct {
	client *http.Client
}

// NewKufar создаёт коллектор Kufar.
func NewKufar() *Kufar {
	return &Kufar{client: &http.Client{Timeout: 20 * time.Second}}
}

func (k *Kufar) Name() string { return "kufar" }

// cat=1010 — продажа квартир; gtsy — гео-фильтр Минска (город);
// sort=lst.d — сначала свежие; cur=USD — цены в долларах.
const kufarURL = "https://api.kufar.by/search-api/v2/search/rendered?" +
	"cat=1010&cur=USD&gtsy=country-belarus~province-minsk~locality-minsk&" +
	"lang=ru&size=30&sort=lst.d"

type kufarResp struct {
	Ads []kufarAd `json:"ads"`
}

type kufarAd struct {
	AdID              int64        `json:"ad_id"`
	Subject           string       `json:"subject"`
	PriceUSD          string       `json:"price_usd"` // в центах, строкой
	AdLink            string       `json:"ad_link"`
	ListTime          string       `json:"list_time"`
	AdParameters      []kufarParam `json:"ad_parameters"`
	AccountParameters []kufarParam `json:"account_parameters"`
}

type kufarParam struct {
	P string `json:"p"`
	V any    `json:"v"` // может быть строкой или числом
}

func (k *Kufar) Fetch(ctx context.Context, f Filter) ([]model.Listing, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, kufarURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 (FlatRadar bot)")

	resp, err := k.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("kufar: запрос: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("kufar: статус %d", resp.StatusCode)
	}

	var data kufarResp
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("kufar: разбор JSON: %w", err)
	}

	out := make([]model.Listing, 0, len(data.Ads))
	for _, a := range data.Ads {
		// price_usd хранится в центах.
		priceUSD := parseAmount(a.PriceUSD) / 100
		if priceUSD < f.PriceMin || (f.PriceMax > 0 && priceUSD > f.PriceMax) {
			continue
		}

		rooms, _ := strconv.Atoi(paramStr(a.AdParameters, "rooms"))
		area, _ := strconv.ParseFloat(paramStr(a.AdParameters, "size"), 64)
		floor := paramStr(a.AdParameters, "floor")
		address := paramStr(a.AccountParameters, "address")
		if address == "" {
			address = a.Subject
		}

		createdAt, _ := time.Parse(time.RFC3339, a.ListTime)

		out = append(out, model.Listing{
			Source:     k.Name(),
			ExternalID: strconv.FormatInt(a.AdID, 10),
			Title:      a.Subject,
			PriceUSD:   priceUSD,
			Rooms:      rooms,
			Area:       area,
			Floor:      floor,
			Address:    address,
			URL:        a.AdLink,
			CreatedAt:  createdAt,
		})
	}
	return out, nil
}

// paramStr достаёт значение параметра по имени p из списка параметров Kufar.
func paramStr(params []kufarParam, name string) string {
	for _, p := range params {
		if p.P != name {
			continue
		}
		switch v := p.V.(type) {
		case string:
			return v
		case float64:
			return strconv.FormatFloat(v, 'f', -1, 64)
		}
	}
	return ""
}
