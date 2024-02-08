package main

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io/ioutil"
	mrand "math/rand"
	"net/http"
	//"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"mu.dev"

	"github.com/go-resty/resty/v2"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"

	"github.com/gomarkdown/markdown"
	"github.com/gomarkdown/markdown/html"
	"github.com/gomarkdown/markdown/parser"
)

const (
	authCookieName  = "__Secure-1PSID"
	maxNumOfAnswers = 3 // bard provides up to 3 answers to each question
)

// configurable values
const (
	timeoutSnim0e = 5  // timeout for the snim0e request (in seconds)
	timeoutQuery  = 15 // timeout for the query request (in seconds)
)

const bardURL string = "https://gemini.google.com/_/BardChatUi/data/assistant.lamda.BardFrontendService/StreamGenerate"

var templates = map[string]string{
	"general": "%s",
	"islam":   "Respond only from qualified Islamic sources like the Quran and Sunnah of the prophet Muhammad and related publications.\n\n%s",
	"news":    "Respond based on current factual news occuring in real time around the world.\n\n%s",
}

var (
	PSID   = os.Getenv("PSID")
	PSIDTS = os.Getenv("PSIDTS")
)

type Answer struct {
	content        string
	conversationID string
	responseID     string
	choiceID       string
}

func (a *Answer) setContent(value string) {
	a.content = value
}

func (a *Answer) setConversationID(value string) {
	a.conversationID = value
}

func (a *Answer) setResponseID(value string) {
	a.responseID = value
}

func (a *Answer) setChoiceID(value string) {
	a.choiceID = value
}

func (a *Answer) getContent() string {
	return a.content
}

func (a *Answer) getConversationID() string {
	return a.conversationID
}

func (a *Answer) getResponseID() string {
	return a.responseID
}

func (a *Answer) getChoiceID() string {
	return a.choiceID
}

// Bard is the main struct for the gemini.google.com API.
type Bard struct {
	PSID       string
	PSIDTS     string
	answers    map[int]*Answer // up to 3 answers per question
	currAnswer int             // current answer
	numAnswers int             // current number of answers
	client     *resty.Client   // resty client
}

// New creates a new Bard instance.
func New(PSID, PSIDTS string) *Bard {
	b := &Bard{
		PSID:    PSID,
		PSIDTS:  PSIDTS,
		answers: make(map[int]*Answer),
	}

	for i := 0; i < maxNumOfAnswers; i++ {
		b.answers[i] = &Answer{}
	}

	b.currAnswer = 0

	return b
}

// Ask asks a question to gemini.google.com.
func (b *Bard) Ask(prompt string) error {
	//prompt = url.QueryEscape(prompt)

	b.createRestClient()

	// Prepare request
	snim0e, err := b.getSnim0eValue()
	if err != nil {
		return err
	}
	session, err := b.createSession(prompt) // will use the current answer for the session
	if err != nil {
		return err
	}
	request, err := b.createRequest(session)
	if err != nil {
		return err
	}

	// Prepare the client
	b.client.SetBaseURL(bardURL)
	b.client.SetTimeout(timeoutQuery * time.Second)
	b.client.SetDoNotParseResponse(true)
	b.client.SetFormData(b.createFormData(snim0e, request))
	b.client.SetQueryParams(b.createBatchExecuteReqParams())

	// Send the request (will reset the current answer to 0)
	return b.doAsk()
}

// GetAnswer returns the current answer.
func (b *Bard) GetAnswer() string {
	return b.getAnswer(b.currAnswer).getContent()
}

// NextAnswer moves to the next answer and returns it.
func (b *Bard) NextAnswer() string {
	b.Next()
	return b.GetAnswer()
}

// PrevAnswer moves to the previous answer and returns it.
func (b *Bard) PrevAnswer() string {
	b.Prev()
	return b.GetAnswer()
}

// Next moves to the next answer.
func (b *Bard) Next() {
	switch b.currAnswer {
	case 0:
		b.currAnswer = 1
	case 1:
		b.currAnswer = 2
	case 2:
		b.currAnswer = 0
	}
}

// Prev moves to the previous answer.
func (b *Bard) Prev() {
	switch b.currAnswer {
	case 0:
		b.currAnswer = 2
	case 1:
		b.currAnswer = 0
	case 2:
		b.currAnswer = 1
	}
}

// Reset resets the bard instance (new conversation).
func (b *Bard) Reset() {
	for i := 0; i < maxNumOfAnswers; i++ {
		b.answers[i] = &Answer{}
	}
	b.currAnswer = 0
}

// GetNumOfAnswers returns the current number of answers.
func (b *Bard) GetNumOfAnswers() int {
	return b.numAnswers
}

//
// Getters and setters
//

func (b *Bard) getAnswer(id int) *Answer {
	return b.answers[id]
}

func (b *Bard) getAllAnswers() []*Answer {
	var values []*Answer
	for i := 0; i < maxNumOfAnswers; i++ {
		values = append(values, b.getAnswer(i))
	}
	return values
}

//
// Bard related functions
//

var headers map[string]string = map[string]string{
	"Host":          "gemini.google.com",
	"X-Same-Domain": "1",
	"User-Agent":    "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/114.0.0.0 Safari/537.36 Edg/114.0.1823.82",
	"Content-Type":  "application/x-www-form-urlencoded;charset=UTF-8",
	"Origin":        "https://gemini.google.com",
	"Referer":       "https://gemini.google.com/",
}

// createRestClient creates a resty client with the needed configuration.
func (b *Bard) createRestClient() {
	b.client = resty.New()
	b.client.SetLogger(Logger{})
	b.client.SetDebug(false)
	b.client.SetHeaders(headers)
	cookies := []*http.Cookie{
		{Name: "__Secure-1PSID", Value: b.PSID},
		{Name: "__Secure-1PSIDTS", Value: b.PSIDTS},
	}
	b.client.SetCookies(cookies)
}

var snim0eRegex = regexp.MustCompile(`SNlM0e\":\"(.*?)\"`)

// getSnim0eValue gets the snim0e value from gemini.google.com.
func (b *Bard) getSnim0eValue() (string, error) {
	// snim0e: AJWyuYX8NLX7SKFihs03g0AoLU-o:1689960334051 (e.g)

	b.client.SetBaseURL("https://gemini.google.com")
	b.client.SetTimeout(timeoutSnim0e * time.Second)

	resp, err := b.client.R().Get("/")
	if err != nil {
		return "", err
	}
	if resp.StatusCode() != 200 {
		return "", fmt.Errorf("status code: %d", resp.StatusCode())
	}

	if len(snim0eRegex.FindStringSubmatch(resp.String())) < 2 {
		return "", fmt.Errorf("could not find snim0e")
	}

	return snim0eRegex.FindStringSubmatch(resp.String())[1], nil
}

// createSession creates the session for the query request.
func (b *Bard) createSession(prompt string) ([]byte, error) {
	session := []interface{}{
		[]string{
			prompt,
		},
		nil,
		[]string{
			b.getAnswer(b.currAnswer).getConversationID(),
			b.getAnswer(b.currAnswer).getResponseID(),
			b.getAnswer(b.currAnswer).getChoiceID(),
		},
	}

	sessionM, err := json.Marshal(session)
	if err != nil {
		return []byte{}, err
	}

	return sessionM, err
}

// createRequest creates the request body for the query request.
func (b *Bard) createRequest(session []byte) ([]byte, error) {
	// reqM: [null,"[[\"Hello.+How+are+you+%3F\"],null,[\"conversationId\",\"responseId\",\"choiceId\"]]"]

	req := []interface{}{
		nil,
		string(session), // stringify marshalled session []byte
	}

	reqM, err := json.Marshal(req)
	if err != nil {
		return []byte{}, err
	}

	return reqM, nil
}

// createFormData creates the form data for the query request.
func (b *Bard) createFormData(snim0e string, request []byte) map[string]string {
	// f.req =  array of envelopes for each payload in the batch
	// at =  XSRF mitigation (time tied to user's google account paired with unix ts)

	formData := map[string]string{
		"f.req": string(request), // stringify marshalled request []byte
		"at":    snim0e,
	}

	return formData
}

// createBatchExecuteReqParams creates a map with needed request parameters.
func (b *Bard) createBatchExecuteReqParams() map[string]string {
	return map[string]string{
		"bl":     "boq_assistant-bard-web-server_20230718.13_p2", // name and version of the backend software handling the requests
		"_reqid": fmt.Sprintf("%d", mrand.Intn(999999)+100000),   // request id (random 4 digits + 100000)
		"rt":     "c",                                            // response type (c = standard, b = protobuf, none = easier json)
	}
}

func (b *Bard) doAsk() error {
	b.currAnswer = 0 // reset current answer

	// Send the request
	response, err := b.client.R().Post("")
	if err != nil {
		return fmt.Errorf("post error: % w", err)
	}
	if response.StatusCode() != 200 {
		return fmt.Errorf("status code: %d", response.StatusCode())
	}

	// Read the response
	bodyBufferBytes := new(bytes.Buffer)
	_, err = bodyBufferBytes.ReadFrom(response.RawResponse.Body)
	if err != nil {
		return fmt.Errorf("read error: %w", err)
	}
	bodyBufferStrJson := strings.Split(bodyBufferBytes.String(), "\n")[3]

	// Parse the response
	var rawResponse [][]interface{}
	err = json.Unmarshal([]byte(bodyBufferStrJson), &rawResponse)
	if err != nil {
		return fmt.Errorf("unmarshal error: %w", err)
	}

	// Parse the answers
	responseStrJson, ok := rawResponse[0][2].(string)
	if !ok {
		return fmt.Errorf("no answer ?")
	}

	for _, answer := range b.getAllAnswers() {
		answer.setConversationID(gjson.Get(responseStrJson, "1.0").String())
		answer.setResponseID(gjson.Get(responseStrJson, "1.1").String())
	}

	promptAnswer := gjson.Get(responseStrJson, "4").Array()

	// Sanity check
	if len(promptAnswer) == 0 {
		return fmt.Errorf("bing not response")
	}

	// Set the current number of answers
	b.numAnswers = len(promptAnswer)

	// Sanity check
	if b.numAnswers > maxNumOfAnswers {
		promptAnswer = promptAnswer[:maxNumOfAnswers]
	}

	// Set the answers
	for i := 0; i < len(promptAnswer); i++ {
		b.getAnswer(i).setChoiceID(promptAnswer[i].Array()[0].String())
		b.getAnswer(i).setContent(promptAnswer[i].Array()[1].Array()[0].String())
	}

	return nil
}

type Logger struct{}

func (l Logger) Debugf(msg string, args ...interface{}) {
	fmt.Printf("DEBUG: "+msg, args...)
}

func (l Logger) Infof(msg string, args ...interface{}) {
	fmt.Printf("INFO: "+msg, args...)
}

func (l Logger) Warnf(msg string, args ...interface{}) {
	fmt.Printf("WARN: "+msg, args...)
}

func (l Logger) Errorf(msg string, args ...interface{}) {
	fmt.Printf("ERROR: "+msg, args...)
}

func (l Logger) Fatalf(msg string, args ...interface{}) {
	fmt.Printf("FATAL: "+msg, args...)
}

var mutex sync.RWMutex
var bards = map[string]*Bard{}

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
	// each user gets a new bard instance
	id := uuid.New().String()
	bard := New(PSID, PSIDTS)

	mutex.Lock()
	bards[id] = bard
	mutex.Unlock()

	w.Write([]byte(`
<html>
  <head>
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Mu Chat</title>
    <style>
      body {
	margin: 0 auto;
	padding: 20px;
	font-family: arial;
	font-size: 14px;
	max-width: 800px;
      }
      #input {
	width: 100%;
	height: 55px;
	position: relative;
      }
      .mu, .you {
        margin-top: 10px;
       }
       #prompt {
         width: calc(100% - 70px);
	 padding: 10px;
       }
       #form {
         position: absolute;
	 bottom: 10px;
	 width: 100%;
	 margin: 0;
       }
       #form button {
         padding: 10px;
       }
       #text {
	 height: calc(100% - 130px);
	 overflow-y: scroll;
       }
       #topics > a {
         border: 1px solid black;
	 border-radius: 5px;
	 padding: 3px;
	 color: black;
	 text-decoration: none;
       }
       .highlight {
         background: grey;
       }
    </style>
  </head>
  <body>
    <div id="topics">
      <a href="#general">General</a>
      <a href="#islam">Islam</a>
      <a href="#news">News</a>
    </div>

    <div id=text><h3>Hello, what can I help you with?</h3></div>

    <div id="input">
      <form id="form" action="/prompt">
        <input id="uuid" name="uuid" type="hidden" value="` + id + `">
        <input id="prompt" name="prompt" placeholder="ask a question" autocomplete="off">
	<input id="category" name="category" type="hidden">
        <button>submit</button>
      </form>
    </div>

    <div id="share">
      <form id="share-form" action="/share">
        <input id="uuid" name="uuid" type="hidden" value="` + id + `">
        <button><small>share</small></button>
      </form>
      <span id="link"></span>
    </div>

    <script>
      var form = document.getElementById("form");
      var text = document.getElementById("text");
      form.addEventListener("submit", function(ev) {
	ev.preventDefault();
        var data = document.getElementById("form");
	var uuid = form.elements["uuid"].value;
        var prompt = form.elements["prompt"].value;
	form.elements["prompt"].value = '';
	text.innerHTML += "<div class=you><small><b>you</b></small><br>" + prompt + "</div>";
	text.scrollTo(0, text.scrollHeight);
	var data = {"uuid": uuid, "prompt": prompt, "markdown": true};

	fetch("/prompt", {
		method: "POST",
		body: JSON.stringify(data),
		headers: {'Content-Type': 'application/json'},
	})
	  .then(res => res.json())
	  .then((rsp) => {
		  //var answer = rsp.answer.replaceAll("\n", "<br>");
		  //answer = answer.replaceAll(/\*\*([A-Za-z0-9]+(\s[A-Za-z0-9]+)*:)\*\*/g, "<b>$1</b>");
		  var answer = rsp.markdown;
		  var height = text.scrollHeight;
		  text.innerHTML += "<div class=mu><small><b>mu</b></small><br>" + answer + "</div>";
		  text.scrollTo(0, height + 20);
	});
	return false;
      });

      // share form
      var sform = document.getElementById("share-form");

      sform.addEventListener("submit", function(ev) {
	ev.preventDefault();
        var link = document.getElementById("link");
	var text = document.getElementById("text")
	var uuid = sform.elements["uuid"].value;
	var data = {"uuid": uuid, "text": text.innerHTML};

	fetch("/share", {
		method: "POST",
		body: JSON.stringify(data),
		headers: {'Content-Type': 'application/json'},
	})
	  .then(res => res.json())
	  .then((rsp) => {
		  link.innerHTML = "<a href=/share?id=" + rsp.id + " target='_blank'>link</a>";
	});

	return false;
      })

      window.addEventListener("hashchange", () => {
        var hash = window.location.hash.replace("#", "");
	var category = document.getElementById("category")
	category.value = hash;

	var el = document.querySelectorAll('#topics a');
	for (let i = 0; i < el.length; i++) {
          el[i].className = '';
	  if (el[i].href.endsWith('#' + hash)) {
            el[i].className = 'highlight';
	  }
	}
      }, false);
    </script>
  </body>
</html>
	`))
}

var Template = `
<html>
  <head>
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Mu Chat</title>
    <style>
      body {
	margin: 0 auto;
	padding: 20px;
	font-family: arial;
	font-size: 14px;
	max-width: 800px;
      }
      #input {
	width: 100%%;
	height: 55px;
	position: relative;
      }
      .mu, .you {
        margin-top: 10px;
       }
       #prompt {
         width: calc(100%% - 70px);
	 padding: 10px;
       }
       #form {
         position: absolute;
	 bottom: 10px;
	 width: 100%%;
	 margin: 0;
       }
       #form button {
         padding: 10px;
       }
       #text {
	 height: calc(100%% - 130px);
	 overflow-y: scroll;
       }
    </style>
  </head>
  <body>
    <div id=text>%s</div>
  </body>
</html>
`

type Req struct {
	UUID     string `json:"uuid"`
	Prompt   string `json:"prompt"`
	Markdown bool   `json:"markdown",omitempty`
	Category string `json:"category",omitempty`
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

	// get the bard
	mutex.RLock()
	bard, ok := bards[id]
	mutex.RUnlock()

	if !ok {
		fmt.Println("bard not found")
		return
	}

	// set question
	q := prompt

	if len(req.Category) > 0 {
		t, ok := templates[req.Category]
		if ok {
			q = fmt.Sprintf(t, prompt)
		}
	}

	// ask the question
	if err := bard.Ask(q); err != nil {
		w.Write([]byte(`can't answer that question`))
		fmt.Println(prompt, "response error:", err)
		return
	}

	answer := bard.GetAnswer()
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
}

var (
	alphanum = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
)

// random generate i length alphanum string
func random(i int) string {
	bytes := make([]byte, i)
	for {
		rand.Read(bytes)
		for i, b := range bytes {
			bytes[i] = alphanum[b%byte(len(alphanum))]
		}
		return string(bytes)
	}
	return uuid.New().String()
}

type Share struct {
	UUID string `json:"uuid"`
	Text string `json:"text"`
}

func shareHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		r.ParseForm()

		id := r.Form.Get("id")
		if len(id) == 0 {
			fmt.Println("missing id")
			return
		}
		// must comply with our id format
		if !regexp.MustCompilePOSIX("^[a-zA-Z0-9]+$").MatchString(id) {
			fmt.Println("did not match regexp")
			return
		}

		file := fmt.Sprintf("chat.%s.enc", id)
		var data string

		// load from the cache
		if err := mu.Load(&data, file); err != nil {
			fmt.Println("Failed to load chat", err)
			return
		}

		html := fmt.Sprintf(Template, data)

		// todo: styling
		w.Write([]byte(html))
	}

	// rest of this function is for sharing
	if r.Method != "POST" {
		return
	}

	b, _ := ioutil.ReadAll(r.Body)
	var req Share
	json.Unmarshal(b, &req)

	if len(req.UUID) == 0 {
		fmt.Println("uuid", req.UUID)
		return
	}
	if len(req.Text) == 0 {
		fmt.Println("no text")
		return
	}

	// generate a short id
	id := random(8)

	// get the text
	text := req.Text

	// write to cache
	file := fmt.Sprintf("chat.%s.enc", id)

	if err := mu.Save(text, file); err != nil {
		fmt.Println("failed to save", err)
		return
	}

	rsp := map[string]interface{}{
		"id": id,
	}

	w.Header().Set("Content-Type", "application/json")
	// write the response
	b, _ = json.Marshal(rsp)
	w.Write(b)
}

func main() {
	if len(PSID) == 0 || len(PSIDTS) == 0 {
		fmt.Println("Missing PSID")
		os.Exit(1)
	}

	http.HandleFunc("/", indexHandler)
	http.HandleFunc("/prompt", promptHandler)
	http.HandleFunc("/share", shareHandler)
	http.ListenAndServe(":8081", nil)
}
