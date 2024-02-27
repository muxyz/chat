package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	//"net/url"
	"sync"

	"mu.dev"

	"github.com/google/uuid"

	"github.com/gomarkdown/markdown"
	"github.com/gomarkdown/markdown/html"
	"github.com/gomarkdown/markdown/parser"
)

type Channel struct {
	Name     string
	Topic    string
	Messages []string
}

var channels = map[string]*Channel{
	"general": new(Channel),
	"islam":   new(Channel),
	"news":    new(Channel),
}

type Command func(string) string

var commands = map[string]Command{}

var updates = make(chan bool, 1)

var mutex sync.RWMutex

func mdToHTML(md []byte) []byte {
	// create markdown parser with extensions
	extensions := parser.CommonExtensions | parser.AutoHeadingIDs | parser.NoEmptyLineBeforeBlock
	p := parser.NewWithExtensions(extensions)
	doc := p.Parse(md)

	// create HTML renderer with extensions
	htmlFlags := html.CommonFlags | html.HrefTargetBlank
	opts := html.RendererOptions{Flags: htmlFlags}
	renderer := html.NewRenderer(opts)

	return markdown.Render(doc, renderer)
}

func indexHandler(w http.ResponseWriter, r *http.Request) {
	id := uuid.New().String()
	channel := "general"

	// get cookie
	c, err := r.Cookie("uuid")
	if err == nil && len(c.Value) > 0 {
		id = c.Value
	} else {
		http.SetCookie(w, &http.Cookie{
			Name:  "uuid",
			Value: id,
		})
	}

	c, err = r.Cookie("channel")
	if err == nil && len(c.Value) > 0 {
		channel = c.Value
	} else {
		http.SetCookie(w, &http.Cookie{
			Name:  "channel",
			Value: channel,
		})
	}

	mutex.Lock()
	ch, ok := channels[channel]
	if !ok {
		ch = new(Channel)
		channels[channel] = ch
	}
	mutex.Unlock()

	// get the channel
	text := ""
	for _, m := range ch.Messages {
		text += fmt.Sprintf("<div class=message>%s</div>", m)
	}

	t := mu.Template("Chat", "Reflections of self", `
      <a href="#general" class="head">General</a>
      <a href="#islam" class="head">Islam</a>
      <a href="#news" class="head">News</a>`, `
    <style>
      #input {
	width: 100%;
	height: 55px;
	position: relative;
      }
       #prompt {
         width: calc(100% - 100px);
	 padding: 10px;
       }
       #form {
	 bottom: 10px;
	 margin: 5px;
       }
       #form button {
         padding: 10px;
       }
       .message {
         padding: 5px 10px;
       }
       #text {
	 height: calc(100% - 140px);
	 overflow-y: scroll;
	 padding-top: 50px;
       }
       .highlight {
         text-decoration: underline;
       }
       @media only screen and (max-width: 600px) {
         #text { padding: 60px 20px 20px 20px; }
	 .message { padding: 5px 0 0 0; }
       }
    </style>

    <div id=text>`+text+`</div>

    <div id="input">
      <form id="form" action="/prompt">
        <input id="uuid" name="uuid" type="hidden" value="`+id+`">
        <input id="prompt" name="prompt" placeholder="ask a question" autocomplete="off">
	<input id="channel" name="channel" type="hidden" value="`+channel+`">
        <button>submit</button>
      </form>
    </div>

    <script>
      var form = document.getElementById("form");
      var text = document.getElementById("text");
      form.addEventListener("submit", function(ev) {
	ev.preventDefault();
        var data = document.getElementById("form");
	var uuid = form.elements["uuid"].value;
        var prompt = form.elements["prompt"].value;
	var channel = form.elements["channel"].value;
	form.elements["prompt"].value = '';
	text.innerHTML += "<div class=message>" + prompt + "</div>";
	text.scrollTo(0, text.scrollHeight);
	var data = {"uuid": uuid, "prompt": prompt, "markdown": true, channel: channel};

	fetch("/prompt", {
		method: "POST",
		body: JSON.stringify(data),
		headers: {'Content-Type': 'application/json'},
	})
	  .then(res => res.json())
	  .then((rsp) => {
		  if (rsp.answer === undefined) {
			return
		  }
		  if (rsp.markdown === undefined) {
			return
		  }
		  var answer = rsp.markdown;
		  var height = text.scrollHeight;
		  text.innerHTML += "<div class=message>" + answer + "</div>";
		  text.scrollTo(0, height + 20);
	});
	return false;
      });

      var hash = window.location.hash.replace("#", "");

      var el = document.querySelectorAll('#nav a');
      for (let i = 0; i < el.length; i++) {
        el[i].className = 'head';
        if (el[i].href.endsWith('#' + hash)) {
          el[i].className = 'highlight head';
        }
      }

      document.cookie = "channel="+"`+channel+`";

      text.scrollTo(0, text.scrollHeight);

      window.addEventListener("hashchange", () => {
        var hash = window.location.hash.replace("#", "");
	var channel = document.getElementById("channel")
	channel.value = hash;
        document.cookie = "channel="+hash;

	window.location.reload();
      }, false);

      function refresh() {
	window.location.reload();
      };

      var refreshTimeout;

      document.getElementById("prompt").onkeypress = function() {
        if (refreshTimeout != undefined) clearTimeout(refreshTimeout);
        refreshTimeout = setTimeout(refresh, 5000);
      };

      refreshTimeout = setTimeout(refresh, 5000);
    </script>
	`)
	w.Write([]byte(t))
}

type Req struct {
	UUID     string `json:"uuid"`
	Prompt   string `json:"prompt"`
	Markdown bool   `json:"markdown",omitempty`
	Channel  string `json:"channel",omitempty`
}

func promptHandler(w http.ResponseWriter, r *http.Request) {
	b, _ := ioutil.ReadAll(r.Body)
	var req Req
	json.Unmarshal(b, &req)

	id := req.UUID
	prompt := req.Prompt

	if len(req.UUID) == 0 {
		fmt.Println("uuid", id)
		return
	}
	if len(req.Prompt) == 0 {
		fmt.Println("no prompt")
		return
	}

	if len(req.Channel) > 0 {
		mutex.Lock()
		c, ok := channels[req.Channel]
		if ok {
			c.Messages = append(c.Messages, prompt)
		}
		mutex.Unlock()
	}

	select {
	case updates <- true:
	default:
	}

	// ask the question
	command, ok := commands[prompt]
	if ok {
		answer := command(prompt)
		markdown := ""

		if req.Markdown {
			markdown = string(mdToHTML([]byte(answer)))
		}

		// get the answer
		rsp := map[string]interface{}{
			"answer":   answer,
			"markdown": markdown,
		}
		b, _ = json.Marshal(rsp)
		w.Header().Set("Content-Type", "application/json")
		w.Write(b)
		return
	}

	w.Write([]byte(`{}`))
}

func load() {
	mutex.Lock()
	mu.Load(&channels, "chat.enc")
	mutex.Unlock()
}

func save() {
	for {
		select {
		case <-updates:
			mutex.RLock()
			mu.Save(channels, "chat.enc")
			mutex.RUnlock()
		}
	}
}

func main() {
	load()

	go save()

	http.HandleFunc("/", indexHandler)
	http.HandleFunc("/prompt", promptHandler)
	http.ListenAndServe(":8081", nil)
}
