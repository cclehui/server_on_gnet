package websocket

import "html/template"

// ClientTemplate 是示例用浏览器客户端页面.
// 模板参数是 ws 地址(例如 ws://127.0.0.1:8081), 由 examples/websocket 按请求 Host 拼出来.
var ClientTemplate = template.Must(template.New("").Parse(`
<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<title>server_on_gnet websocket demo</title>
<style>
  body { font-family: -apple-system, "Segoe UI", Arial, sans-serif; margin: 24px; }
  #status { color: #444; margin-bottom: 12px; }
  #output { font-family: Menlo, Consolas, monospace; font-size: 13px;
            border: 1px solid #ddd; border-radius: 4px; padding: 8px;
            height: 420px; overflow-y: auto; background: #fafafa; }
  #output div { white-space: pre-wrap; word-break: break-all; padding: 1px 0; }
  .ts { color: #999; margin-right: 6px; }
  .sent { color: #0a58ca; }
  .recv { color: #146c43; }
  .meta { color: #6c757d; font-weight: 600; }
  .err  { color: #b02a37; font-weight: 600; }
  input { width: 420px; padding: 4px; }
  button { padding: 4px 10px; margin-right: 4px; }
</style>
<script>
window.addEventListener("load", function (evt) {
    var target = "{{.}}";
    var output = document.getElementById("output");
    var input = document.getElementById("input");
    var status = document.getElementById("status");
    var ws = null;

    document.getElementById("target").textContent = target;

    // 每行都带时间戳, 这样 "点了没反应" 时能看出是没连上还是没收到回包
    function print(cls, label, message) {
        var line = document.createElement("div");
        var ts = document.createElement("span");
        ts.className = "ts";
        ts.textContent = new Date().toLocaleTimeString();

        var body = document.createElement("span");
        body.className = cls;
        body.textContent = label + message;

        line.appendChild(ts);
        line.appendChild(body);
        output.appendChild(line);
        output.scrollTop = output.scrollHeight;
    }

    function setButtons(connected) {
        document.getElementById("open").disabled = connected;
        document.getElementById("close").disabled = !connected;
        document.getElementById("send").disabled = !connected;
        status.textContent = connected ? "已连接: " + target : "未连接";
    }

    function connect() {
        if (ws && (ws.readyState === WebSocket.OPEN || ws.readyState === WebSocket.CONNECTING)) {
            return;
        }

        print("meta", "", "连接 " + target + " ...");
        ws = new WebSocket(target);

        ws.onopen = function () {
            print("meta", "OPEN ", "连接建立");
            setButtons(true);
        };

        ws.onmessage = function (evt) {
            print("recv", "RECV ", evt.data);
        };

        ws.onclose = function (evt) {
            // code/reason 是排查服务端主动断开(例如空闲回收 1001)的关键信息
            print("meta", "CLOSE", " code=" + evt.code + " reason=" + (evt.reason || "(empty)") +
                " clean=" + evt.wasClean);
            ws = null;
            setButtons(false);
        };

        // 浏览器出于安全不会把失败原因放进 onerror, 只能提示去看服务端日志/控制台
        ws.onerror = function () {
            print("err", "ERROR", " 连接出错, 原因见浏览器控制台或服务端日志");
        };
    }

    function send() {
        if (!ws || ws.readyState !== WebSocket.OPEN) {
            print("err", "ERROR", " 还没连接, 先点 Open");
            return;
        }

        var text = input.value;
        ws.send(text);
        print("sent", "SEND ", text);
    }

    document.getElementById("open").onclick = connect;
    document.getElementById("send").onclick = send;
    document.getElementById("close").onclick = function () {
        if (ws) {
            ws.close(1000, "client close");
        }
    };

    input.addEventListener("keydown", function (evt) {
        if (evt.key === "Enter") {
            send();
        }
    });

    setButtons(false);

    // 打开页面就直接连, 免得以为页面坏了
    connect();
});
</script>
</head>
<body>
<h3>server_on_gnet websocket demo</h3>
<div id="status"></div>
<div>目标地址: <code id="target"></code></div>
<p>
  <button id="open">Open</button>
  <button id="close">Close</button>
  <input id="input" type="text" value="Hello world!">
  <button id="send">Send</button>
</p>
<p>服务端是 echo: 回包格式为 <code>response is :&lt;payload&gt;, 当前时间:&lt;ts&gt;</code></p>
<div id="output"></div>
</body>
</html>

`))
