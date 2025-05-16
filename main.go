package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type WeatherResponse struct {
	Name string `json:"name"`
	Main struct {
		Temp     float64 `json:"temp"`
		Humidity int     `json:"humidity"`
	} `json:"main"`
	Weather []struct {
		Main        string `json:"main"`
		Description string `json:"description"`
	} `json:"weather"`
}

type ForecastResponse struct {
	List []struct {
		Dt   int64 `json:"dt"`
		Main struct {
			Temp float64 `json:"temp"`
		} `json:"main"`
		Weather []struct {
			Description string `json:"description"`
		} `json:"weather"`
	} `json:"list"`
}

var (
	userCities      = make(map[int64]string)
	userSchedules   = make(map[int64][]string)
	waitingForCity  = make(map[int64]bool)
	waitingForTime  = make(map[int64]bool)
	waitingToRemove = make(map[int64]bool)
	mutex           sync.Mutex
)

func main() {
	telegramToken := os.Getenv("TELEGRAM_TOKEN")
	weatherApiKey := os.Getenv("WEATHER_API_KEY")

	bot, err := tgbotapi.NewBotAPI(telegramToken)
	if err != nil {
		log.Panic(err)
	}
	bot.Debug = false
	log.Printf("✅ Бот запущено як %s", bot.Self.UserName)
	go func() {
		http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintln(w, "✅ Bot is running")
		})
		log.Fatal(http.ListenAndServe(":10000", nil)) // Render автоматично визначить PORT
	}()

	go startScheduler(bot, weatherApiKey)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := bot.GetUpdatesChan(u)

	for update := range updates {
		if update.Message == nil {
			continue
		}

		userID := update.Message.Chat.ID
		text := strings.TrimSpace(update.Message.Text)

		if waitingForCity[userID] {
			mutex.Lock()
			userCities[userID] = text
			waitingForCity[userID] = false
			mutex.Unlock()
			bot.Send(tgbotapi.NewMessage(userID, fmt.Sprintf("✅ Місто \"%s\" збережено!", text)))
			continue
		}

		if waitingForTime[userID] {
			mutex.Lock()
			userSchedules[userID] = append(userSchedules[userID], text)
			waitingForTime[userID] = false
			mutex.Unlock()
			bot.Send(tgbotapi.NewMessage(userID, fmt.Sprintf("✅ Час %s додано до розсилки!", text)))
			continue
		}

		if waitingToRemove[userID] {
			mutex.Lock()
			times := userSchedules[userID]
			var newTimes []string
			for _, t := range times {
				if t != text {
					newTimes = append(newTimes, t)
				}
			}
			userSchedules[userID] = newTimes
			waitingToRemove[userID] = false
			mutex.Unlock()
			bot.Send(tgbotapi.NewMessage(userID, fmt.Sprintf("🗑 Видалено розсилку о %s", text)))
			continue
		}

		switch text {
		case "/start":
			menu := tgbotapi.NewReplyKeyboard(
				tgbotapi.NewKeyboardButtonRow(
					tgbotapi.NewKeyboardButton("📍 Задати місто"),
					tgbotapi.NewKeyboardButton("🌦 Показати погоду"),
				),
				tgbotapi.NewKeyboardButtonRow(
					tgbotapi.NewKeyboardButton("📅 Прогноз на день"),
					tgbotapi.NewKeyboardButton("📬 Розсилка прогнозу"),
				),
				tgbotapi.NewKeyboardButtonRow(
					tgbotapi.NewKeyboardButton("❌ Видалити розсилку"),
				),
			)
			msg := tgbotapi.NewMessage(userID, "Привіт! Обери дію з меню:")
			msg.ReplyMarkup = menu
			bot.Send(msg)

		case "📍 Задати місто":
			waitingForCity[userID] = true
			bot.Send(tgbotapi.NewMessage(userID, "Введи назву міста:"))

		case "📅 Прогноз на день":
			city, ok := userCities[userID]
			if !ok {
				bot.Send(tgbotapi.NewMessage(userID, "Спочатку задай місто."))
				continue
			}
			forecast, err := getDailyForecast(city, weatherApiKey)
			if err != nil {
				bot.Send(tgbotapi.NewMessage(userID, "Помилка отримання прогнозу."))
				continue
			}
			bot.Send(tgbotapi.NewMessage(userID, forecast))

		case "🌦 Показати погоду":
			city, ok := userCities[userID]
			if !ok {
				bot.Send(tgbotapi.NewMessage(userID, "Спочатку задай місто."))
				continue
			}
			weather, err := getWeather(city, weatherApiKey)
			if err != nil {
				bot.Send(tgbotapi.NewMessage(userID, "Не вдалося отримати погоду."))
				continue
			}
			bot.Send(tgbotapi.NewMessage(userID, weather))

		case "📬 Розсилка прогнозу":
			waitingForTime[userID] = true
			bot.Send(tgbotapi.NewMessage(userID, "Введи час розсилки у форматі HH:MM (за Києвом):"))

		case "❌ Видалити розсилку":
			mutex.Lock()
			if len(userSchedules[userID]) == 0 {
				mutex.Unlock()
				bot.Send(tgbotapi.NewMessage(userID, "У тебе немає жодної розсилки."))
				continue
			}
			waitingToRemove[userID] = true
			sched := strings.Join(userSchedules[userID], ", ")
			mutex.Unlock()
			bot.Send(tgbotapi.NewMessage(userID, "Введи точний час розсилки, яку хочеш видалити:\n"+sched))

		default:
			bot.Send(tgbotapi.NewMessage(userID, "Невідома команда. Використай меню."))
		}
	}
}

func getWeather(city string, weatherApiKey string) (string, error) {
	url := fmt.Sprintf("https://api.openweathermap.org/data/2.5/weather?q=%s&appid=%s&units=metric&lang=ua", city, weatherApiKey)

	resp, err := http.Get(url)
	if err != nil {
		return "", fmt.Errorf("помилка запиту: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var data WeatherResponse
	err = json.Unmarshal(body, &data)
	if err != nil || len(data.Weather) == 0 {
		return "", fmt.Errorf("не вдалося отримати дані для %s", city)
	}

	text := fmt.Sprintf("📍 Погода в місті %s:\n🌡 %.1f°C\n💧 Вологість: %d%%\n☁️ %s (%s)\n\n🧠 Коментар:\n",
		data.Name, data.Main.Temp, data.Main.Humidity, data.Weather[0].Description, data.Weather[0].Main)

	switch {
	case data.Main.Temp <= -10:
		text += "🥶 Надворі так холодно, що навіть Wi-Fi замерз!"
	case data.Main.Temp <= 0:
		text += "🧥 Вдягайся як капуста — шар за шаром."
	case data.Main.Temp <= 10:
		text += "🌀 Краще залишайся вдома з чаєм."
	case data.Main.Temp <= 20:
		text += "🌤 Легенький светрик не завадить."
	case data.Main.Temp <= 30:
		text += "😎 Ідеально! Йди ловити сонце."
	default:
		text += "🔥 Надворі жарко. Тримайся в тіні й пий воду."
	}

	switch data.Weather[0].Main {
	case "Rain":
		text += "\n☔ Парасоля — твій найкращий друг сьогодні."
	case "Snow":
		text += "\n❄ Головне — не лизати металеві предмети."
	case "Clear":
		text += "\n🌞 Можна засмагати, але не перегрівайся."
	case "Thunderstorm":
		text += "\n⛈ Краще не виходити з дому без причини."
	case "Clouds":
		text += "\n🌫 Ідеально для філософських думок про сенс життя."
	}

	return text, nil
}

func getDailyForecast(city, apiKey string) (string, error) {
	geoURL := fmt.Sprintf("http://api.openweathermap.org/geo/1.0/direct?q=%s&limit=1&appid=%s", city, apiKey)
	resp, err := http.Get(geoURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var geoData []struct {
		Lat float64 `json:"lat"`
		Lon float64 `json:"lon"`
	}
	body, _ := io.ReadAll(resp.Body)
	json.Unmarshal(body, &geoData)
	if len(geoData) == 0 {
		return "", fmt.Errorf("місто не знайдено")
	}

	lat, lon := geoData[0].Lat, geoData[0].Lon
	forecastURL := fmt.Sprintf("https://api.openweathermap.org/data/2.5/forecast?lat=%f&lon=%f&appid=%s&units=metric&lang=ua", lat, lon, apiKey)
	resp, err = http.Get(forecastURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ = io.ReadAll(resp.Body)
	var forecast ForecastResponse
	json.Unmarshal(body, &forecast)

	currentDate := time.Now().Format("2006-01-02")
	result := fmt.Sprintf("📅 Прогноз на день для %s:\n", city)

	for _, entry := range forecast.List {
		entryTime := time.Unix(entry.Dt, 0)
		if entryTime.Format("2006-01-02") == currentDate {
			desc := strings.ToLower(entry.Weather[0].Description)
			emoji := "🌡"
			switch {
			case strings.Contains(desc, "дощ"):
				emoji = "🌧"
			case strings.Contains(desc, "сніг"):
				emoji = "❄️"
			case strings.Contains(desc, "сонячно"):
				emoji = "☀️"
			case strings.Contains(desc, "ясно"):
				emoji = "🌞"
			case strings.Contains(desc, "гроза"):
				emoji = "⛈"
			case strings.Contains(desc, "хмар"):
				emoji = "☁️"
			case strings.Contains(desc, "туман"):
				emoji = "🌫"
			case strings.Contains(desc, "вітер"):
				emoji = "💨"
			}
			result += fmt.Sprintf("🕒 %s: %s %.1f°C — %s\n",
				entryTime.Format("15:04"),
				emoji,
				entry.Main.Temp,
				entry.Weather[0].Description)
		}
	}

	if result == "" {
		return "", fmt.Errorf("немає даних на сьогодні")
	}
	return result, nil
}

func startScheduler(bot *tgbotapi.BotAPI, apiKey string) {
	ticker := time.NewTicker(1 * time.Minute)
	for {
		<-ticker.C
		now := time.Now().In(time.FixedZone("Kyiv", 3*60*60))
		current := now.Format("15:04")

		mutex.Lock()
		for userID, times := range userSchedules {
			for _, t := range times {
				if t == current {
					if city, ok := userCities[userID]; ok {
						if forecast, err := getDailyForecast(city, apiKey); err == nil {
							msg := tgbotapi.NewMessage(userID, "🔔 Автоматична розсилка:\n"+forecast)
							bot.Send(msg)
						}
					}
				}
			}
		}
		mutex.Unlock()
	}
}
