use anyhow::{Context, Result, bail};
use reqwest::{Client, Method, StatusCode, header};
use serde_json::Value;
use std::time::Duration;
use url::Url;

const GRAPH_BASE: &str = "https://graph.microsoft.com/v1.0";

pub(crate) struct GraphClient {
    client: Client,
    base: Url,
}

impl GraphClient {
    pub(crate) fn new(access_token: &str) -> Result<Self> {
        let mut authorization = header::HeaderValue::from_str(&format!("Bearer {access_token}"))
            .context("Identity provider returned an invalid access token header")?;
        authorization.set_sensitive(true);
        let mut headers = header::HeaderMap::new();
        headers.insert(header::AUTHORIZATION, authorization);
        let client = Client::builder()
            .default_headers(headers)
            .timeout(Duration::from_secs(10))
            .redirect(reqwest::redirect::Policy::none())
            .retry(reqwest::retry::never())
            .build()
            .context("Could not initialize Microsoft Graph client")?;
        Self::with_client(client, Url::parse(GRAPH_BASE)?)
    }

    // Offline construction seam: callers provide a no-redirect client with a timeout.
    // There is deliberately no command-line or environment endpoint override.
    pub(crate) fn with_client(client: Client, mut base: Url) -> Result<Self> {
        let graph_origin = base.scheme() == "https"
            && base.host_str() == Some("graph.microsoft.com")
            && base.port_or_known_default() == Some(443);
        let loopback_origin = base.scheme() == "http"
            && match base.host() {
                Some(url::Host::Ipv4(ip)) => ip.is_loopback(),
                Some(url::Host::Ipv6(ip)) => ip.is_loopback(),
                _ => false,
            };
        if !(graph_origin || loopback_origin)
            || !base.username().is_empty()
            || base.password().is_some()
            || base.query().is_some()
            || base.fragment().is_some()
        {
            bail!("Invalid Microsoft Graph base URL");
        }
        let path = base.path().trim_end_matches('/').to_owned();
        base.set_path(&path);
        Ok(Self { client, base })
    }

    pub(crate) fn url(&self, path: &str) -> Result<Url> {
        // Concatenation keeps /applications beneath /v1.0, unlike Url::join.
        let url = Url::parse(&format!(
            "{}/{}",
            self.base.as_str().trim_end_matches('/'),
            path.trim_start_matches('/')
        ))
        .context("Invalid Microsoft Graph request path")?;
        self.validate_next_link(url.as_str())
    }

    pub(crate) fn validate_next_link(&self, link: &str) -> Result<Url> {
        let url = Url::parse(link).context("Graph returned an invalid next-page link")?;
        if url.origin() != self.base.origin()
            || !url.username().is_empty()
            || url.password().is_some()
            || url.fragment().is_some()
        {
            bail!("Graph returned an untrusted next-page link");
        }
        Ok(url)
    }

    async fn request(
        &self,
        method: Method,
        url: Url,
        body: Option<&Value>,
    ) -> Result<reqwest::Response> {
        let mut request = self.client.request(method.clone(), url.clone());
        if let Some(body) = body {
            request = request.json(body);
        }
        request
            .send()
            .await
            .with_context(|| format!("{method} {} failed", request_path(&url)))
    }

    async fn response_text(method: &Method, response: reqwest::Response) -> Result<String> {
        let status = response.status();
        let path = request_path(response.url());
        let body = response.text().await.with_context(|| {
            format!(
                "{method} {path} HTTP {}: could not read response",
                status.as_u16()
            )
        })?;
        if !status.is_success() {
            bail!(
                "{method} {path} failed with HTTP {}: {body}",
                status.as_u16()
            );
        }
        Ok(body)
    }

    async fn response_json(method: &Method, response: reqwest::Response) -> Result<Value> {
        let path = request_path(response.url());
        let text = Self::response_text(method, response).await?;
        serde_json::from_str(&text)
            .with_context(|| format!("{method} {path} returned invalid JSON"))
    }

    pub(crate) async fn get_application(&self, id: &str) -> Result<Option<Value>> {
        let paths = [
            format!("/applications/{}", encode(id)),
            format!("/applications(appId='{}')", encode(&id.replace('\'', "''"))),
        ];
        for path in paths {
            let response = self.request(Method::GET, self.url(&path)?, None).await?;
            if response.status() == StatusCode::NOT_FOUND {
                continue;
            }
            let app = Self::response_json(&Method::GET, response).await?;
            require_string(&app, "id")?;
            require_string(&app, "appId")?;
            return Ok(Some(app));
        }
        Ok(None)
    }

    pub(crate) async fn get_url(&self, url: &Url) -> Result<Value> {
        let url = self.validate_next_link(url.as_str())?;
        let response = self.request(Method::GET, url, None).await?;
        Self::response_json(&Method::GET, response).await
    }

    pub(crate) async fn post(&self, path: &str, body: &Value) -> Result<Value> {
        let response = self
            .request(Method::POST, self.url(path)?, Some(body))
            .await?;
        Self::response_json(&Method::POST, response).await
    }

    pub(crate) async fn patch(&self, path: &str, body: &Value) -> Result<()> {
        let response = self
            .request(Method::PATCH, self.url(path)?, Some(body))
            .await?;
        Self::response_text(&Method::PATCH, response).await?;
        Ok(())
    }

    pub(crate) async fn delete(&self, path: &str) -> Result<()> {
        let response = self.request(Method::DELETE, self.url(path)?, None).await?;
        Self::response_text(&Method::DELETE, response).await?;
        Ok(())
    }
}

fn request_path(url: &Url) -> String {
    match url.query() {
        Some(query) => format!("{}?{query}", url.path()),
        None => url.path().to_owned(),
    }
}

pub(crate) fn require_string<'a>(value: &'a Value, key: &str) -> Result<&'a str> {
    value
        .as_object()
        .and_then(|object| object.get(key))
        .and_then(Value::as_str)
        .with_context(|| format!("Graph response requires an object with string '{key}'"))
}

pub(crate) fn encode(value: &str) -> String {
    const HEX: &[u8; 16] = b"0123456789ABCDEF";
    let mut encoded = String::with_capacity(value.len());
    for byte in value.bytes() {
        if byte.is_ascii_alphanumeric() || matches!(byte, b'-' | b'.' | b'_' | b'~') {
            encoded.push(char::from(byte));
        } else {
            encoded.push('%');
            encoded.push(char::from(HEX[usize::from(byte >> 4)]));
            encoded.push(char::from(HEX[usize::from(byte & 15)]));
        }
    }
    encoded
}

#[cfg(test)]
pub(crate) mod tests {
    use super::*;
    use serde_json::json;
    use std::{
        collections::VecDeque,
        io::{Read, Write},
        net::TcpListener,
        sync::{
            Arc,
            atomic::{AtomicBool, Ordering},
            mpsc,
        },
        thread::{self, JoinHandle},
    };

    type FixtureResponse = (u16, String, Vec<(String, String)>);

    pub(crate) struct Fixture {
        pub(crate) base: Url,
        requests: mpsc::Receiver<String>,
        stop: Arc<AtomicBool>,
        worker: Option<JoinHandle<()>>,
    }

    impl Fixture {
        pub(crate) fn new(responses: Vec<FixtureResponse>) -> Self {
            Self::with_responses(|_| responses)
        }

        pub(crate) fn with_responses(make: impl FnOnce(&Url) -> Vec<FixtureResponse>) -> Self {
            let listener = TcpListener::bind("127.0.0.1:0").unwrap();
            listener.set_nonblocking(true).unwrap();
            let base =
                Url::parse(&format!("http://{}/v1.0", listener.local_addr().unwrap())).unwrap();
            let responses = make(&base);
            let (captured, requests) = mpsc::channel();
            let stop = Arc::new(AtomicBool::new(false));
            let stopping = Arc::clone(&stop);
            let worker = thread::spawn(move || {
                let mut responses = VecDeque::from(responses);
                while !stopping.load(Ordering::Relaxed) {
                    let (mut stream, _) = match listener.accept() {
                        Ok(connection) => connection,
                        Err(error) if error.kind() == std::io::ErrorKind::WouldBlock => {
                            thread::sleep(Duration::from_millis(2));
                            continue;
                        }
                        Err(error) => panic!("fixture accept failed: {error}"),
                    };
                    // Accepted sockets inherit O_NONBLOCK on macOS.
                    stream.set_nonblocking(false).unwrap();
                    stream
                        .set_read_timeout(Some(Duration::from_secs(2)))
                        .unwrap();
                    stream
                        .set_write_timeout(Some(Duration::from_secs(2)))
                        .unwrap();
                    let mut bytes = Vec::new();
                    let mut buffer = [0; 4096];
                    loop {
                        let count = stream.read(&mut buffer).unwrap();
                        assert_ne!(count, 0, "incomplete request");
                        bytes.extend_from_slice(&buffer[..count]);
                        if let Some(end) = bytes.windows(4).position(|part| part == b"\r\n\r\n") {
                            let header = std::str::from_utf8(&bytes[..end]).unwrap();
                            let length = header
                                .lines()
                                .find_map(|line| {
                                    let (name, value) = line.split_once(':')?;
                                    name.eq_ignore_ascii_case("content-length")
                                        .then(|| value.trim().parse::<usize>().unwrap())
                                })
                                .unwrap_or(0);
                            if bytes.len() >= end + 4 + length {
                                break;
                            }
                        }
                    }
                    captured.send(String::from_utf8(bytes).unwrap()).unwrap();
                    let (status, body, headers) =
                        responses
                            .pop_front()
                            .unwrap_or((500, "unexpected request".into(), vec![]));
                    write!(
                        stream,
                        "HTTP/1.1 {status} Fixture\r\nContent-Length: {}\r\nConnection: close\r\n",
                        body.len()
                    )
                    .unwrap();
                    for (name, value) in headers {
                        write!(stream, "{name}: {value}\r\n").unwrap();
                    }
                    write!(stream, "\r\n{body}").unwrap();
                }
            });
            Self {
                base,
                requests,
                stop,
                worker: Some(worker),
            }
        }

        // Drains requests recorded so far; responses are sent only after recording.
        pub(crate) fn take_requests(&self) -> Vec<String> {
            self.requests.try_iter().collect()
        }

        pub(crate) fn client(&self) -> GraphClient {
            let client = Client::builder()
                .no_proxy()
                .redirect(reqwest::redirect::Policy::none())
                .timeout(Duration::from_secs(2))
                .default_headers({
                    let mut headers = header::HeaderMap::new();
                    let mut token = header::HeaderValue::from_static("Bearer fake-token");
                    token.set_sensitive(true);
                    headers.insert(header::AUTHORIZATION, token);
                    headers
                })
                .build()
                .unwrap();
            GraphClient::with_client(client, self.base.clone()).unwrap()
        }
    }

    impl Drop for Fixture {
        fn drop(&mut self) {
            self.stop.store(true, Ordering::Relaxed);
            let result = self.worker.take().unwrap().join();
            if !thread::panicking() {
                result.unwrap();
            }
        }
    }

    fn response(status: u16, body: &str) -> (u16, String, Vec<(String, String)>) {
        (status, body.to_owned(), vec![])
    }

    #[tokio::test]
    async fn lookup_falls_back_only_on_not_found_and_encodes_odata_quotes() {
        let fixture = Fixture::new(vec![
            response(404, "missing"),
            response(200, r#"{"id":"object","appId":"client"}"#),
        ]);
        let app = fixture
            .client()
            .get_application("a'b /?#")
            .await
            .unwrap()
            .unwrap();
        assert_eq!(app["id"], "object");
        let requests = fixture.take_requests();
        assert_eq!(requests.len(), 2);
        assert!(requests[0].starts_with("GET /v1.0/applications/a%27b%20%2F%3F%23 HTTP/1.1"));
        assert!(
            requests[1]
                .starts_with("GET /v1.0/applications(appId='a%27%27b%20%2F%3F%23') HTTP/1.1")
        );
    }

    #[tokio::test]
    async fn two_not_found_responses_are_absent() {
        let fixture = Fixture::new(vec![response(404, "first"), response(404, "second")]);
        assert!(
            fixture
                .client()
                .get_application("missing")
                .await
                .unwrap()
                .is_none()
        );
        assert_eq!(fixture.take_requests().len(), 2);
    }

    #[tokio::test]
    async fn lookup_never_falls_back_on_forbidden_or_malformed_success() {
        for (status, body) in [
            (403, "denied-body"),
            (200, "[]"),
            (200, r#"{"id":"object"}"#),
            (200, r#"{"id":null,"appId":"client"}"#),
            (200, r#"{"id":"object","appId":5}"#),
            (200, "not-json"),
        ] {
            let fixture = Fixture::new(vec![response(status, body)]);
            let error = fixture
                .client()
                .get_application("reference")
                .await
                .unwrap_err()
                .to_string();
            assert_eq!(fixture.take_requests().len(), 1);
            if status == 403 {
                assert!(error.contains("GET /v1.0/applications/reference"));
                assert!(error.contains("403"));
                assert!(error.contains("denied-body"));
            }
        }
    }

    #[tokio::test]
    async fn redirects_are_errors_without_following_location() {
        let destination = Fixture::new(vec![]);
        let source = Fixture::new(vec![(
            302,
            "redirect-body".into(),
            vec![("Location".into(), destination.base.to_string())],
        )]);
        let error = source
            .client()
            .get_application("reference")
            .await
            .unwrap_err()
            .to_string();
        assert!(error.contains("302"));
        assert!(error.contains("redirect-body"));
        assert_eq!(source.take_requests().len(), 1);
        assert!(destination.take_requests().is_empty());
    }

    #[tokio::test]
    async fn unsafe_page_url_is_rejected_before_sending() {
        let source = Fixture::new(vec![]);
        let destination = Fixture::new(vec![]);
        assert!(source.client().get_url(&destination.base).await.is_err());
        let mut credentials = source.base.clone();
        credentials.set_username("user").unwrap();
        assert!(source.client().get_url(&credentials).await.is_err());
        assert!(source.take_requests().is_empty());
        assert!(destination.take_requests().is_empty());
    }

    #[test]
    fn pagination_restricts_origin_credentials_and_scheme() {
        let graph = GraphClient::new("fake-token").unwrap();
        assert!(
            graph
                .validate_next_link(
                    "https://GRAPH.MICROSOFT.COM:443/v1.0/applications?$skiptoken=x"
                )
                .is_ok()
        );
        for link in [
            "/v1.0/applications",
            "http://graph.microsoft.com/v1.0/applications",
            "https://graph.microsoft.com:444/v1.0/applications",
            "https://graph.microsoft.com.evil.test/v1.0/applications",
            "https://user@graph.microsoft.com/v1.0/applications",
            "https://user:pass@graph.microsoft.com/v1.0/applications",
            "https://graph.microsoft.com/v1.0/applications#fragment",
        ] {
            assert!(graph.validate_next_link(link).is_err(), "accepted {link}");
        }
        let fixture = Fixture::new(vec![]);
        let client = fixture.client();
        assert!(client.validate_next_link(fixture.base.as_str()).is_ok());
        assert!(
            client
                .validate_next_link("http://127.0.0.1:1/v1.0/applications")
                .is_err()
        );
    }

    #[tokio::test]
    async fn patch_accepts_empty_success_but_post_requires_json() {
        let fixture = Fixture::new(vec![response(204, ""), response(201, "")]);
        let graph = fixture.client();
        graph
            .patch("/applications/object", &json!({"displayName":"new"}))
            .await
            .unwrap();
        assert!(graph.post("/applications", &json!({})).await.is_err());
        let requests = fixture.take_requests();
        assert!(requests[0].starts_with("PATCH /v1.0/applications/object "));
        assert!(requests[0].ends_with(r#"{"displayName":"new"}"#));
        assert!(requests[1].starts_with("POST /v1.0/applications "));
    }
}
