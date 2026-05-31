package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"flatradar/internal/model"
)

// Onliner — коллектор объявлений с realt.onliner.by через публичный JSON API.
type Onliner struct {
	client *http.Client
}

// NewOnliner создаёт коллектор Onliner.
func NewOnliner() *Onliner {
	return &Onliner{client: &http.Client{Timeout: 20 * time.Second}}
}

func (o *Onliner) Name() string { return "onliner" }

// onlinerResp — форма ответа https://pk.api.onliner.by/search/apartments.
type onlinerResp struct {
	Apartments []onlinerApartment `json:"apartments"`
	Total      int                `json:"total"`
}

type onlinerApartment struct {
	ID       int64 `json:"id"`
	Location struct {
		Address string `json:"address"`
	} `json:"location"`
	Price struct {
		Converted struct {
			USD struct {
				Amount string `json:"amount"`
			} `json:"USD"`
		} `json:"converted"`
	} `json:"price"`
	Photo          string `json:"photo"`
	NumberOfRooms  int    `json:"number_of_rooms"`
	Floor          int    `json:"floor"`
	NumberOfFloors int    `json:"number_of_floors"`
	Area           struct {
		Total float64 `json:"total"`
	} `json:"area"`
	URL       string `json:"url"`
	CreatedAt string `json:"created_at"`
}

func (o *Onliner) Fetch(ctx context.Context, f Filter) ([]model.Listing, error) {
	const url = "https://pk.api.onliner.by/search/apartments?order=created_at:desc&page=1"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 (FlatRadar bot)")

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("onliner: запрос: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("onliner: статус %d", resp.StatusCode)
	}

	var data onlinerResp
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("onliner: разбор JSON: %w", err)
	}

	out := make([]model.Listing, 0, len(data.Apartments))
	for _, a := range data.Apartments {
		priceUSD := parseAmount(a.Price.Converted.USD.Amount)

		// Фильтр по цене.
		if priceUSD < f.PriceMin || (f.PriceMax > 0 && priceUSD > f.PriceMax) {
			continue
		}
		// Грубый фильтр по городу по адресу.
		// TODO: Onliner возвращает всю Беларусь — для точного Минска
		// нужно передавать geo-bounds города в запрос.
		if f.City != "" && !strings.Contains(a.Location.Address, f.City) {
			continue
		}

		createdAt, _ := time.Parse(time.RFC3339, a.CreatedAt)

		out = append(out, model.Listing{
			Source:     o.Name(),
			ExternalID: strconv.FormatInt(a.ID, 10),
			Title:      a.Location.Address,
			PriceUSD:   priceUSD,
			Rooms:      a.NumberOfRooms,
			Area:       a.Area.Total,
			Floor:      fmt.Sprintf("%d/%d", a.Floor, a.NumberOfFloors),
			Address:    a.Location.Address,
			URL:        a.URL,
			Photo:      a.Photo,
			CreatedAt:  createdAt,
		})
	}
	return out, nil
}

// parseAmount превращает строку вида "65000.00" в целое число долларов.
func parseAmount(s string) int {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return int(v)
}
