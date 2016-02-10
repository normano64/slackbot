package slackbot

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/gorilla/websocket"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

var (
	api    = "https://slack.com/api/"
	dialer = websocket.Dialer{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
	}
)

type Client struct {
	token     string
	ws        *websocket.Conn
	handlers  []handler
	incomming chan map[string]interface{}
	count     int
	Id        string
	Name      string
}

type handler struct {
	pattern     string
	handlerFunc func(io.Writer, Response)
}

type Response struct {
	User    string
	Time    string
	Channel string
	Data    []string
}

func New(token string) (*Client, error) {
	if token == "" {
		return nil, errors.New("Missing slack api token")
	}

	c := &Client{
		token:    token,
		count:    0,
		handlers: make([]handler, 0),
	}

	return c, nil
}

func (c *Client) connect() error {
	data := make(map[string]interface{})

	resp, err := http.PostForm(api+"rtm.start",
		url.Values{
			"token":         {c.token},
			"simple_latest": {},
			"no_unreads":    {},
		})
	if err != nil {
		return err
	}

	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	err = json.Unmarshal(body, &data)
	if err != nil {
		return err
	}

	if data["ok"].(bool) == true {
		ws, _, err := dialer.Dial(data["url"].(string), nil)
		if err != nil {
			return err
		}

		c.ws = ws

		c.Id = data["self"].(map[string]interface{})["id"].(string)
		c.Name = data["self"].(map[string]interface{})["name"].(string)
	} else {
		return errors.New(data["error"].(string))
	}
	return nil
}

func (c *Client) read() error {
	for {
		data := make(map[string]interface{})

		err := c.ws.ReadJSON(&data)
		if err != nil {
			return err
		}

		if _, ok := data["type"]; ok {
			c.incomming <- data
		}
	}

	return nil
}
func (c *Client) write(messageId int, channel string, text string) error {
	data := map[string]interface{}{
		"id":      messageId,
		"type":    "message",
		"channel": channel,
		"text":    text,
	}

	err := c.ws.WriteJSON(&data)
	if err != nil {
		return err
	}

	return nil
}

func (c *Client) Start() error {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	err := c.connect()
	if err != nil {
		return err
	}

	c.incomming = make(chan map[string]interface{})
	go c.read()

	for {
		select {
		case ev := <-c.incomming:
			switch ev["type"].(string) {
			case "hello":
				log.Printf("Hello\n")
			case "message":
				go c.callHandler(ev["channel"].(string), ev["user"].(string),
					ev["ts"].(string), ev["text"].(string))
				log.Printf("%s@%s: %s \n", ev["user"], ev["channel"], ev["text"])
			case "user_typing":
				break
			case "channel_joined":
				break
			case "channel_left":
				break
			case "presence_change":
				break
			case "reconnect_url":
				break
			default:
				log.Printf("Unknown: %s\n", ev)
			}
		}
	}

	return nil
}

func (c *Client) AddHandler(pattern string, handlerFunc func(io.Writer, Response)) error {
	c.handlers = append(c.handlers, handler{
		pattern:     pattern,
		handlerFunc: handlerFunc,
	})

	return nil
}

func (c *Client) callHandler(channel string, user string, time string, text string) error {
	if (strings.HasPrefix(text, "<@"+c.Id+">") || strings.HasPrefix(text, c.Name) || strings.HasPrefix(channel, "D")) && user != c.Id {
		w := new(bytes.Buffer)

		text = c.removeHighlight(text)

		which, data, err := c.parseString(text)
		if err != nil {
			return err
		} else if which == -1 {
			return nil
		}

		response := Response{
			Channel: channel,
			User:    user,
			Time:    time,
			Data:    data,
		}

		c.handlers[which].handlerFunc(w, response)
		c.count++
		err = c.write(c.count, channel, w.String())
		if err != nil {
			return err
		}
	}

	return nil
}

func (c *Client) removeHighlight(text string) string {
	if strings.HasPrefix(text, "<@"+c.Id+">") {
		text = text[len("<@"+c.Id+">"):len(text)]
	} else if strings.HasPrefix(text, c.Name) {
		text = text[len(c.Id):len(text)]
	}

	if strings.HasPrefix(text, ":") {
		text = text[len(":"):len(text)]
	}

	return text
}

func (c *Client) parseString(text string) (int, []string, error) {
	which := -1
	for key, handler := range c.handlers {
		if b, _ := regexp.MatchString(handler.pattern, text); b {
			which = key
		}
	}
	log.Printf("%d\n", which)
	if which >= 0 {
		re := regexp.MustCompile(c.handlers[which].pattern)
		matches := re.FindAllStringSubmatch(text, -1)

		data := make([]string, cap(matches[0])-1)
		for i := 1; i < cap(matches[0]); i++ {
			data[i-1] = matches[0][i]
		}

		return which, data, nil
	}

	return -1, nil, nil
}
