package gurlib2

import (
	"fmt"
	"github.com/robertkrimen/otto"
	"github.com/satori/go.uuid"
	"io/ioutil"
	"net/http"
	"strings"
	"time"
)

type JsEngine struct {
	VM *otto.Otto
	c  *http.Client
}

func NewJsEngine(c *http.Client) *JsEngine {
	js := &JsEngine{
		VM: otto.New(),
		c:  c,
	}

	register(js.VM, js)
	return js
}

func (j *JsEngine) JsGurl(call otto.FunctionCall) otto.Value {

	o, err := call.Argument(0).Export()
	if err != nil {
		fmt.Printf("err:%v\n", o)
		return otto.Value{}
	}

	m := o.(map[string]interface{})

	g := Gurl{
		Client: j.c,
	}
	g.MemInit()
	for k, v := range m {
		switch strings.ToLower(k) {
		case "h":
			h, ok := v.([]string)
			if ok {
				g.H = h
			}
		case "method":
			method, ok := v.(string)
			if ok {
				g.Method = method
			}
		case "f":
			f, ok := v.([]string)
			if ok {
				g.F = f
			}

		case "url":
			url, ok := v.(string)
			if ok {
				g.Url = url
			}

		case "o":
			o, ok := v.(string)
			if ok {
				g.O = o
			}

		case "mf":
			mf, ok := v.([]string)
			if ok {
				formCache := []FormVal{}
				for _, v := range mf {

					parseMF(v, &formCache)
				}
				fmt.Printf("--->%#v\n", formCache)
				g.GurlCore.FormCache =
					append(g.GurlCore.FormCache, formCache...)
			}
		}
	}

	rsp, _ := g.sendExec()
	for k := range m {
		delete(m, k)
	}

	m["status_code"] = rsp.StatusCode
	m["body"] = string(rsp.Body)
	m["err"] = rsp.Err

	result, err := j.VM.ToValue(m)
	if err != nil {
		fmt.Printf("err:%s\n", err)
	}
	return result
}

func JsReadFile(call otto.FunctionCall) otto.Value {
	f := call.Argument(0).String()

	all, err := ioutil.ReadFile(f)
	if err != nil {
		panic(err.Error())
	}

	result, _ := otto.ToValue(string(all))
	return result
}

func JsLen(call otto.FunctionCall) otto.Value {
	a := call.Argument(0).String()

	result, _ := otto.ToValue(len(a))
	return result
}

func JsSleep(call otto.FunctionCall) otto.Value {
	t := call.Argument(0).String()
	t = strings.TrimSpace(t)
	tv := 0
	company := time.Second
	companyStr := ""

	company = time.Second
	fmt.Sscanf(t, "%d%s", &tv, &companyStr)
	switch companyStr {
	case "ms":
		company = time.Millisecond
	case "s":
		company = time.Second
	case "m":
		company = time.Minute
	case "h":
		company = time.Hour
	}

	time.Sleep(time.Duration(tv) * company)
	return otto.Value{}
}

func JsUUID(call otto.FunctionCall) otto.Value {
	u1 := uuid.Must(uuid.NewV4())
	result, _ := otto.ToValue(u1.String())
	return result
}

func JsExtract(call otto.FunctionCall) otto.Value {
	all := call.Argument(0).String()

	start, err := call.Argument(1).ToInteger()
	if err != nil {
		fmt.Printf("%s\n", err)
		return otto.Value{}
	}

	end, err := call.Argument(2).ToInteger()
	if err != nil {
		fmt.Printf("%s\n", err)
		return otto.Value{}
	}

	result, _ := otto.ToValue(all[start:end])
	return result
}

func register(vm *otto.Otto, js *JsEngine) {
	vm.Set("gurl_readfile", JsReadFile)
	vm.Set("gurl_len", JsLen)
	vm.Set("gurl_sleep", JsSleep)
	vm.Set("gurl_uuid", JsUUID)
	vm.Set("gurl", js.JsGurl)
	vm.Set("gurl_extract", JsExtract)
}
