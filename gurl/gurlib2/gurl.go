package gurlib2

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/NaihongGuo/flag"
	"io"
	"io/ioutil"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type FormVal struct {
	Tag   string
	Fname string
	Body  []byte
}

type GurlCore struct {
	Method string `json:"method,omitempty"`

	J   []string `json:"J,omitempty"`
	F   []string `json:"F,omitempty"`
	H   []string `json:"H,omitempty"` // http header
	Url string   `json:"url,omitempty"`
	O   string   `json:"o,omitempty"`

	Jfa []string `json:"Jfa,omitempty"`

	FormCache []FormVal `json:"-"`

	Body []byte `json:"body,omitempty"`
}

func parseVal(bodyJson map[string]interface{}, key, val string) {
	if val == "{}" {
		bodyJson[key] = map[string]interface{}{}
		return
	}

	f, err := strconv.ParseFloat(val, 0)
	if err == nil {
		bodyJson[key] = f
		return
	}

	i, err := strconv.ParseInt(val, 0, 0)
	if err == nil {
		bodyJson[key] = i
		return
	}

	b, err := strconv.ParseBool(val)
	if err == nil {
		bodyJson[key] = b
		return
	}

	bodyJson[key] = val
}

func parseVal2(bodyJson map[string]interface{}, key, val string) {
	bodyJson[key] = val
}

func toJson(J []string, bodyJson map[string]interface{}) {
	for _, v := range J {
		pos := strings.Index(v, ":")
		if pos == -1 {
			continue
		}

		key := v[:pos]
		val := v[pos+1:]

		if pos := strings.Index(key, "."); pos != -1 {
			keys := strings.Split(key, ".")

			parseValCb := parseVal2
			if strings.HasPrefix(val, "=") {
				val = val[1:]
				parseValCb = parseVal
			}

			type jsonObj map[string]interface{}

			curMap := bodyJson

			for i, v := range keys {
				if len(keys)-1 == i {
					parseValCb(curMap, v, val)
					break
				}

				vv, ok := curMap[v]
				if !ok {
					vv = jsonObj{}
					curMap[v] = vv
				}

				curMap = vv.(jsonObj)

			}
			continue
		}

		if val[0] != '=' {
			bodyJson[key] = val
			continue
		}

		if len(key) == 1 {
			continue
		}

		val = val[1:]
		parseVal(bodyJson, key, val)

	}
}

func form(F []string, fm *[]FormVal) {

	fileds := [2]string{}
	formVals := []FormVal{}

	for _, v := range F {

		fileds[0], fileds[1] = "", ""

		pos := strings.Index(v, "=")
		if pos == -1 {
			continue
		}

		fileds[0], fileds[1] = v[:pos], v[pos+1:]

		if strings.HasPrefix(fileds[1], "@") {
			fname := fileds[1][1:]

			fd, err := os.Open(fname)
			if err != nil {
				log.Fatalf("open file fail:%v\n", err)
			}

			body, err2 := ioutil.ReadAll(fd)
			if err != nil {
				log.Fatalf("read body fail:%v\n", err2)
			}

			formVals = append(formVals, FormVal{Tag: fileds[0], Fname: fname, Body: body})

			fd.Close()
		} else {
			formVals = append(formVals, FormVal{Tag: fileds[0], Body: []byte(fileds[1])})
		}

		//F[i] = fileds[0]
	}

	*fm = append(*fm, formVals...)
}

func jsonFromAppend(JF []string, fm *[]FormVal) {

	JFMap := map[string][]string{}
	fileds := [2]string{}
	formVals := []FormVal{}

	for _, v := range JF {

		fileds[0], fileds[1] = "", ""

		pos := strings.Index(v, "=")
		if pos == -1 {
			continue
		}

		fileds[0], fileds[1] = v[:pos], v[pos+1:]

		v, _ := JFMap[fileds[0]]
		JFMap[fileds[0]] = append(v, fileds[1])
	}

	for k, v := range JFMap {

		bodyJson := map[string]interface{}{}

		toJson(v, bodyJson)

		body, err := json.Marshal(&bodyJson)

		if err != nil {
			log.Fatalf("marsahl fail:%s\n", err)
			return
		}

		formVals = append(formVals, FormVal{Tag: k, Body: body})
	}

	*fm = append(*fm, formVals...)
}

func (b *GurlCore) MemInit() {

	if len(b.J) > 0 {
		bodyJson := map[string]interface{}{}

		toJson(b.J, bodyJson)

		body, err := json.Marshal(&bodyJson)
		if err != nil {
			log.Fatalf("marsahl fail:%s\n", err)
			return
		}

		b.Body = body
	}

	b.FormCache = []FormVal{}

	if len(b.Jfa) > 0 {
		jsonFromAppend(b.Jfa, &b.FormCache)
	}

	if len(b.F) > 0 {
		form(b.F, &b.FormCache)
	}
}

func (b *GurlCore) Multipart(client *http.Client) (rsp *http.Response) {

	var req *http.Request

	req, errChan, err := b.MultipartNew()
	if err != nil {
		fmt.Printf("multipart new fail:%s\n", err)
		return
	}

	b.HeadersAdd(req)

	c := client
	rsp, err = c.Do(req)
	if err != nil {
		fmt.Printf("client do fail:%s\n", err)
		return
	}

	if err := <-errChan; err != nil {
		fmt.Printf("error:%s\n", err)
		return nil
	}

	return rsp
}

func (b *GurlCore) MultipartNew() (*http.Request, chan error, error) {

	var err error

	pipeReader, pipeWriter := io.Pipe()
	errChan := make(chan error, 10)
	writer := multipart.NewWriter(pipeWriter)

	go func() {

		defer pipeWriter.Close()

		var part io.Writer

		for _, fv := range b.FormCache {

			k := fv.Tag

			fname := fv.Fname

			if len(fname) == 0 {
				part, err = writer.CreateFormField(k)
				part.Write([]byte(fv.Body))
				continue
			}

			body := bytes.NewBuffer(fv.Body)

			part, err = writer.CreateFormFile(k, filepath.Base(fname))
			if err != nil {
				errChan <- err
				return
			}

			if _, err = io.Copy(part, body); err != nil {
				errChan <- err
				return
			}
		}

		errChan <- writer.Close()

	}()

	var req *http.Request
	req, err = http.NewRequest(b.Method, b.Url, pipeReader)
	if err != nil {
		fmt.Printf("http neq request:%s\n", err)
		return nil, errChan, err
	}

	req.Header.Add("Content-Type", writer.FormDataContentType())

	return req, errChan, nil
}

func (b *GurlCore) HeadersAdd(req *http.Request) {

	for _, v := range b.H {

		headers := strings.Split(v, ":")

		if len(headers) != 2 {
			continue
		}

		headers[0] = strings.TrimSpace(headers[0])
		headers[1] = strings.TrimSpace(headers[1])

		req.Header.Add(headers[0], headers[1])
	}
}

func (b *GurlCore) WriteFile(rsp *http.Response, body []byte) {
	fd, err := os.Create(b.O)
	if err != nil {
		return
	}
	defer fd.Close()

	if len(body) > 0 {
		fd.Write(body)
	}

	io.Copy(fd, rsp.Body)
}

func (b *GurlCore) BodyRequest(client *http.Client) (rsp *http.Response) {

	var (
		err error
		req *http.Request
	)

	body := bytes.NewBuffer(b.Body)
	req, err = http.NewRequest(b.Method, b.Url, body)
	if err != nil {
		return
	}

	b.HeadersAdd(req)

	c := client

	rsp, err = c.Do(req)
	if err != nil {
		return
	}

	return rsp
}

type Gurl struct {
	*http.Client `json:"-"`

	GurlCore
}

type Response struct {
	StatusCode int    `json:"status_code"`
	Err        string `json:"err"`
	Body       []byte `json:"body"`
}

//todo reflect copy
func MergeCmd(cfCmd *Gurl, cmd *Gurl, tactics string) {
	switch tactics {
	case "append":
		if len(cmd.Url) > 0 {
			cfCmd.Url = cmd.Url
		}
	case "set":
		*cfCmd = *cmd

	}
}

func (g *Gurl) Send() {
	g.send(g.Client)
}

func (g *GurlCore) send(client *http.Client) {
	var (
		rsp     *http.Response
		body    []byte
		err     error
		needVar bool
	)

	if len(g.Method) == 0 {
		g.Method = "GET"
		if len(g.FormCache) > 0 {
			g.Method = "POST"
		}
	}

	if len(g.FormCache) > 0 {
		rsp = g.Multipart(client)
	} else {
		rsp = g.BodyRequest(client)
	}

	if rsp == nil {
		return
	}

	defer rsp.Body.Close()

	if len(g.Url) > 0 || needVar {

		body, err = ioutil.ReadAll(rsp.Body)
		if err != nil {
			return
		}

	}

	if len(g.O) > 0 {
		g.WriteFile(rsp, body)
		goto last
	}

	if len(body) > 0 {
		os.Stdout.Write(body)
		goto last
	}

	io.Copy(os.Stdout, rsp.Body)

last:
}

func (g *Gurl) writeBytes(rsp *http.Response, all []byte) {
	fd, err := os.Create(g.O)
	if err != nil {
		return
	}
	defer fd.Close()

	fd.Write(all)
}

func (g *Gurl) NotMultipartExec() (*Response, error) {
	var rsp *http.Response
	var req *http.Request
	var err error

	req, err = http.NewRequest(g.Method, g.Url, nil)
	if err != nil {
		return &Response{Err: err.Error()}, err
	}

	gurlRsp := &Response{}
	g.HeadersAdd(req)

	rsp, err = g.Client.Do(req)
	if err != nil {
		return &Response{Err: err.Error()}, err
	}

	defer rsp.Body.Close()
	gurlRsp.Body, err = ioutil.ReadAll(rsp.Body)
	if err != nil {
		return &Response{Err: err.Error()}, err
	}

	if len(g.O) > 0 {
		g.writeBytes(rsp, gurlRsp.Body)
	}

	return gurlRsp, nil
}

func (g *Gurl) MultipartExec() (*Response, error) {

	var rsp *http.Response
	var req *http.Request

	req, errChan, err := g.MultipartNew()
	if err != nil {
		fmt.Printf("multipart new fail:%s\n", err)
		return &Response{Err: err.Error()}, err
	}

	gurlRsp := &Response{}
	g.HeadersAdd(req)

	rsp, err = g.Client.Do(req)
	if err != nil {
		fmt.Printf("client do fail:%s:URL(%s)\n", err, req.URL)
		return &Response{Err: err.Error()}, err
	}

	defer rsp.Body.Close()

	if err := <-errChan; err != nil {
		fmt.Printf("error:%s\n", err)
		return &Response{Err: err.Error()}, err
	}

	gurlRsp.Body, err = ioutil.ReadAll(rsp.Body)
	if err != nil {
		fmt.Printf("ioutil.Read:%s\n", err)
		return &Response{Err: err.Error()}, err
	}

	if len(g.O) > 0 {

		g.writeBytes(rsp, gurlRsp.Body)
	}

	gurlRsp.StatusCode = rsp.StatusCode

	return gurlRsp, nil
}

func (g *Gurl) sendExec() (*Response, error) {
	if len(g.Method) == 0 {
		g.Method = "GET"
		if len(g.FormCache) > 0 {
			g.Method = "POST"
		}
	}

	if len(g.FormCache) > 0 {
		return g.MultipartExec()
	}

	return g.NotMultipartExec()
}

func parseMF(mf string, formCache *[]FormVal) {
	pos := strings.Index(mf, "=")
	if pos == -1 {
		return
	}

	fv := FormVal{}

	fv.Tag = mf[:pos]
	fv.Body = []byte(mf[pos+1:])
	fv.Fname = "test"
	*formCache = append(*formCache, fv)
}

func ExecSlice(cmd []string) (*Response, error) {

	commandlLine := flag.NewFlagSet(cmd[0], flag.ExitOnError)
	headers := commandlLine.StringSlice("H", []string{}, "Pass custom header LINE to server (H)")
	forms := commandlLine.StringSlice("F", []string{}, "Specify HTTP multipart POST data (H)")
	output := commandlLine.String("o", "", "Write to FILE instead of stdout")
	method := commandlLine.String("X", "", "Specify request command to use")
	memForms := commandlLine.StringSlice("mF", []string{}, "Specify HTTP multipart POST data (H)")
	url := commandlLine.String("url", "", "Specify a URL to fetch")

	commandlLine.Parse(cmd[1:])

	as := commandlLine.Args()

	transport := http.Transport{
		DisableKeepAlives: true,
	}

	u := *url
	if u == "" {
		u = as[0]
	}

	g := Gurl{
		Client: &http.Client{
			Transport: &transport,
		},
		GurlCore: GurlCore{
			Method: *method,
			F:      *forms,
			H:      *headers,
			O:      *output,
			Url:    u,
		},
	}

	g.MemInit()

	formCache := []FormVal{}
	for _, v := range *memForms {

		parseMF(v, &formCache)
	}

	g.GurlCore.FormCache = append(g.GurlCore.FormCache, formCache...)

	return g.sendExec()
}
