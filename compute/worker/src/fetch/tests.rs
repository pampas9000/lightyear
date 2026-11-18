use super::*;
use std::collections::HashMap;
use std::sync::atomic::{AtomicUsize, Ordering};
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::TcpListener;

const PNG: &[u8] = b"\x89PNG\r\n\x1A\n\0\0\0\rIHDR not really a png";

fn ip(s: &str) -> IpAddr {
    s.parse().unwrap()
}

#[test]
fn only_global_unicast_addresses_are_public() {
    let refused = [
        // IPv4
        "0.0.0.0",
        "0.1.2.3",
        "10.0.0.1",
        "100.64.0.1",
        "100.127.255.254",
        "127.0.0.1",
        "127.255.255.255",
        "169.254.169.254",
        "172.16.0.1",
        "172.31.255.255",
        "192.0.0.1",
        "192.0.2.1",
        "192.88.99.1",
        "192.168.1.1",
        "198.18.0.1",
        "198.19.255.255",
        "198.51.100.1",
        "203.0.113.1",
        "224.0.0.1",
        "239.255.255.250",
        "240.0.0.1",
        "255.255.255.255",
        // IPv6
        "::",
        "::1",
        "::ffff:127.0.0.1",
        "::ffff:8.8.8.8",
        "::127.0.0.1",
        "64:ff9b::7f00:1",
        "64:ff9b:1::1",
        "100::1",
        "fc00::1",
        "fd12:3456::1",
        "fe80::1",
        "fec0::1",
        "ff02::1",
        "2001::1",
        "2001:0:4136:e378:8000:63bf:3fff:fdd2",
        "2001:db8::1",
        "2002:7f00:1::1",
        "3fff::1",
        "4000::1",
    ];
    for addr in refused {
        assert!(!is_public(ip(addr)), "{addr} must not be public");
    }
    let public = [
        "1.1.1.1",
        "8.8.8.8",
        "93.184.215.14",
        "100.63.255.255",
        "100.128.0.0",
        "172.15.255.255",
        "172.32.0.0",
        "192.0.3.1",
        "198.20.0.1",
        "223.255.255.254",
        "2606:4700:4700::1111",
        "2001:4860:4860::8888",
        "2a00:1450:4001::1",
    ];
    for addr in public {
        assert!(is_public(ip(addr)), "{addr} must be public");
    }
}

#[test]
fn allow_list_adds_networks() {
    let policy = FetchPolicy::parse(" 127.0.0.0/8, ,fd00::/8,10.1.2.3 ").unwrap();
    assert_eq!(policy.allow.len(), 3);
    assert!(policy.allows(ip("127.0.0.1")));
    assert!(policy.allows(ip("127.200.0.9")));
    assert!(policy.allows(ip("fd00::5")));
    assert!(policy.allows(ip("10.1.2.3")));
    assert!(!policy.allows(ip("10.1.2.4")));
    assert!(!policy.allows(ip("192.168.0.1")));
    // The other family never matches.
    assert!(!policy.allows(ip("::ffff:127.0.0.1")));
    assert!(policy.allows(ip("8.8.8.8")));

    assert!(FetchPolicy::parse("").unwrap().allow.is_empty());
    assert!(FetchPolicy::parse("0.0.0.0/0").unwrap().allows(ip("10.0.0.1")));
    for bad in ["127.0.0.0/33", "::/129", "localhost", "10.0.0.0/x", "10.0.0/8"] {
        assert!(FetchPolicy::parse(bad).is_err(), "{bad} must not parse");
    }
}

#[test]
fn sniffs_image_types() {
    assert_eq!(sniff(b"\xFF\xD8\xFF\xE0rest"), Some("image/jpeg"));
    assert_eq!(sniff(PNG), Some("image/png"));
    assert_eq!(sniff(b"RIFF\0\0\0\0WEBPVP8 "), Some("image/webp"));
    assert_eq!(sniff(b"\0\0\0\x1cftypavif\0\0\0\0"), Some("image/avif"));
    assert_eq!(sniff(b"\0\0\0\x18ftypheic\0\0\0\0"), Some("image/heic"));
    assert_eq!(sniff(b"\xFF\x0A"), Some("image/jxl"));
    assert_eq!(sniff(b"<html>"), None);
    assert_eq!(sniff(b""), None);
}

/// A test server's answer to one request.
enum Reply {
    /// A complete response; the connection is closed after it.
    Full(Vec<u8>),
    /// Bytes written before the server goes silent without closing.
    Stall(Vec<u8>),
}

fn respond(status: &str, headers: &[(&str, &str)], body: &[u8]) -> Reply {
    let mut raw = format!("HTTP/1.1 {status}\r\nConnection: close\r\n");
    for (name, value) in headers {
        raw.push_str(&format!("{name}: {value}\r\n"));
    }
    if !headers.iter().any(|(n, _)| n.eq_ignore_ascii_case("transfer-encoding")) {
        raw.push_str(&format!("Content-Length: {}\r\n", body.len()));
    }
    raw.push_str("\r\n");
    let mut raw = raw.into_bytes();
    raw.extend_from_slice(body);
    Reply::Full(raw)
}

fn redirect(location: &str) -> Reply {
    respond("302 Found", &[("Location", location)], b"")
}

struct Server {
    addr: SocketAddr,
    hits: Arc<AtomicUsize>,
}

impl Server {
    fn url(&self, path: &str) -> String {
        format!("http://{}{path}", self.addr)
    }

    fn hits(&self) -> usize {
        self.hits.load(Ordering::SeqCst)
    }
}

/// An HTTP/1.1 server on 127.0.0.1 answering each request by its path.
async fn serve(handler: impl Fn(&str, u16) -> Reply + Send + Sync + 'static) -> Server {
    let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
    let addr = listener.local_addr().unwrap();
    let hits = Arc::new(AtomicUsize::new(0));
    let handler = Arc::new(handler);
    let counter = hits.clone();
    tokio::spawn(async move {
        loop {
            let Ok((mut socket, _)) = listener.accept().await else {
                return;
            };
            counter.fetch_add(1, Ordering::SeqCst);
            let handler = handler.clone();
            tokio::spawn(async move {
                let mut head = Vec::new();
                let mut buf = [0u8; 1024];
                while !head.windows(4).any(|w| w == b"\r\n\r\n") {
                    match socket.read(&mut buf).await {
                        Ok(0) | Err(_) => return,
                        Ok(n) => head.extend_from_slice(&buf[..n]),
                    }
                }
                let head = String::from_utf8_lossy(&head);
                let path = head.split_whitespace().nth(1).unwrap_or("/").to_owned();
                match handler(&path, addr.port()) {
                    Reply::Full(raw) => {
                        let _ = socket.write_all(&raw).await;
                        let _ = socket.shutdown().await;
                    }
                    Reply::Stall(raw) => {
                        let _ = socket.write_all(&raw).await;
                        tokio::time::sleep(Duration::from_secs(30)).await;
                    }
                }
            });
        }
    });
    Server { addr, hits }
}

/// Resolves the names in `names`, and everything else with the system.
struct FakeLookup(HashMap<&'static str, Vec<IpAddr>>);

impl Lookup for FakeLookup {
    fn lookup(&self, host: &str) -> LookupFuture {
        match self.0.get(host) {
            Some(ips) => {
                let addrs = ips.iter().map(|&ip| SocketAddr::new(ip, 0)).collect();
                Box::pin(async move { Ok(addrs) })
            }
            None => SystemLookup.lookup(host),
        }
    }
}

fn fetcher(allow: &str) -> Fetcher {
    let lookup = FakeLookup(HashMap::from([
        ("internal.test", vec![ip("10.1.2.3")]),
        ("metadata.test", vec![ip("169.254.169.254"), ip("fd00:ec2::254")]),
        ("loopback.test", vec![ip("127.0.0.1")]),
    ]));
    Fetcher::with_lookup(
        FetchPolicy::parse(allow).unwrap(),
        &FetchSettings {
            connect_timeout: Duration::from_secs(2),
            read_timeout: Duration::from_millis(300),
        },
        Arc::new(lookup),
    )
    .unwrap()
}

fn image_server() -> impl Fn(&str, u16) -> Reply + Send + Sync + 'static {
    |path, port| {
        if let Some(n) = path.strip_prefix("/hops/") {
            let n: u32 = n.parse().unwrap();
            return if n == 0 {
                respond("200 OK", &[("Content-Type", "image/png")], PNG)
            } else {
                redirect(&format!("/hops/{}", n - 1))
            };
        }
        match path {
            "/img.png" => respond("200 OK", &[("Content-Type", "image/png")], PNG),
            "/declared" => respond("200 OK", &[("Content-Type", "image/x-thing; q=1")], b"opaque"),
            "/to-other-loopback" => redirect(&format!("http://127.0.0.2:{port}/img.png")),
            "/to-internal-name" => redirect(&format!("http://internal.test:{port}/img.png")),
            "/to-ipv6-loopback" => redirect(&format!("http://[::1]:{port}/img.png")),
            "/to-file" => redirect("file:///etc/passwd"),
            "/no-location" => respond("302 Found", &[], b""),
            "/big" => respond("200 OK", &[], &[0u8; 2000]),
            "/big-chunked" => {
                let mut body = Vec::new();
                for _ in 0..4 {
                    body.extend_from_slice(b"1F4\r\n");
                    body.extend_from_slice(&[0u8; 500]);
                    body.extend_from_slice(b"\r\n");
                }
                body.extend_from_slice(b"0\r\n\r\n");
                respond("200 OK", &[("Transfer-Encoding", "chunked")], &body)
            }
            "/exact" => respond("200 OK", &[], &[0u8; 1000]),
            "/empty" => respond("200 OK", &[], b""),
            "/stall-body" => Reply::Stall(b"HTTP/1.1 200 OK\r\nContent-Length: 100\r\n\r\nabc".to_vec()),
            "/stall-head" => Reply::Stall(Vec::new()),
            "/404" => respond("404 Not Found", &[], b"no"),
            "/403" => respond("403 Forbidden", &[], b"no"),
            "/429" => respond("429 Too Many Requests", &[], b"later"),
            "/500" => respond("500 Internal Server Error", &[], b"oops"),
            "/503" => respond("503 Service Unavailable", &[], b"later"),
            _ => respond("404 Not Found", &[], b""),
        }
    }
}

#[track_caller]
fn assert_rejected(result: Result<Fetched, ComputeError>) -> String {
    match result {
        Err(ComputeError::UrlRejected(message)) => message,
        other => panic!("expected url_rejected, got {other:?}"),
    }
}

#[track_caller]
fn assert_failed(result: Result<Fetched, ComputeError>, retryable: bool) {
    match result {
        Err(err @ ComputeError::FetchFailed { .. }) => {
            assert_eq!(err.code(), "fetch_failed");
            assert_eq!(err.retryable(), retryable, "{err}");
        }
        other => panic!("expected fetch_failed, got {other:?}"),
    }
}

#[tokio::test]
async fn ip_literals_are_checked_before_connecting() {
    let server = serve(image_server()).await;
    let port = server.addr.port();
    let f = fetcher("");
    for url in [
        server.url("/img.png"),
        format!("http://127.1:{port}/img.png"),
        format!("http://2130706433:{port}/img.png"),
        format!("http://0x7f.1:{port}/img.png"),
        format!("http://[::1]:{port}/img.png"),
        format!("http://[::ffff:127.0.0.1]:{port}/img.png"),
        "http://10.0.0.1/img.png".into(),
        "http://169.254.169.254/latest/meta-data/".into(),
        "https://[fd00::1]/".into(),
        "http://0.0.0.0/".into(),
    ] {
        let message = assert_rejected(f.fetch(&url, 1000).await);
        assert!(message.contains("not a public address"), "{url}: {message}");
    }
    assert_eq!(server.hits(), 0, "no connection may be made");
}

#[tokio::test]
async fn names_resolving_to_private_addresses_are_rejected() {
    let server = serve(image_server()).await;
    let port = server.addr.port();
    let f = fetcher("");
    for host in ["localhost", "loopback.test", "internal.test", "metadata.test"] {
        let url = format!("http://{host}:{port}/img.png");
        let message = assert_rejected(f.fetch(&url, 1000).await);
        assert!(message.contains("not a public address"), "{url}: {message}");
    }
    assert_eq!(server.hits(), 0);
}

#[tokio::test]
async fn allow_list_lets_local_addresses_through() {
    let server = serve(image_server()).await;
    let port = server.addr.port();
    let f = fetcher("127.0.0.0/8");
    for url in [
        server.url("/img.png"),
        format!("http://localhost:{port}/img.png"),
        format!("http://loopback.test:{port}/img.png"),
    ] {
        let got = f.fetch(&url, 1000).await.unwrap();
        assert_eq!(&got.bytes[..], PNG);
        assert_eq!(got.mime, "image/png");
    }
    // Still only that network.
    assert_rejected(f.fetch(&format!("http://internal.test:{port}/img.png"), 1000).await);
    // Unknown content keeps a declared image type, or becomes octet-stream.
    let got = f.fetch(&server.url("/declared"), 1000).await.unwrap();
    assert_eq!(got.mime, "image/x-thing");
    let got = f.fetch(&server.url("/exact"), 1000).await.unwrap();
    assert_eq!(got.mime, "application/octet-stream");
}

#[tokio::test]
async fn redirects_are_checked_at_every_hop() {
    let server = serve(image_server()).await;
    let f = fetcher("127.0.0.1/32");
    for path in ["/to-other-loopback", "/to-internal-name", "/to-ipv6-loopback"] {
        let message = assert_rejected(f.fetch(&server.url(path), 1000).await);
        assert!(message.contains("not a public address"), "{path}: {message}");
    }
    let message = assert_rejected(f.fetch(&server.url("/to-file"), 1000).await);
    assert!(message.contains("scheme"), "{message}");
    assert_failed(f.fetch(&server.url("/no-location"), 1000).await, false);
}

#[tokio::test]
async fn at_most_three_redirects_are_followed() {
    let server = serve(image_server()).await;
    let f = fetcher("127.0.0.0/8");
    let got = f.fetch(&server.url("/hops/3"), 1000).await.unwrap();
    assert_eq!(&got.bytes[..], PNG);
    let before = server.hits();
    let message = assert_rejected(f.fetch(&server.url("/hops/4"), 1000).await);
    assert!(message.contains("redirects"), "{message}");
    assert_eq!(server.hits() - before, 4, "the fourth redirect is not followed");
}

#[tokio::test]
async fn bodies_over_the_limit_are_abandoned() {
    let server = serve(image_server()).await;
    let f = fetcher("127.0.0.0/8");
    for path in ["/big", "/big-chunked"] {
        let message = assert_rejected(f.fetch(&server.url(path), 1000).await);
        assert!(message.contains("1000-byte limit"), "{path}: {message}");
    }
    assert_eq!(f.fetch(&server.url("/exact"), 1000).await.unwrap().bytes.len(), 1000);
    assert_failed(f.fetch(&server.url("/empty"), 1000).await, false);
}

#[tokio::test]
async fn timeouts_are_retryable() {
    let server = serve(image_server()).await;
    let f = fetcher("127.0.0.0/8");
    for path in ["/stall-head", "/stall-body"] {
        let started = std::time::Instant::now();
        assert_failed(f.fetch(&server.url(path), 1000).await, true);
        assert!(started.elapsed() < Duration::from_secs(5), "{path} took {:?}", started.elapsed());
    }
}

#[tokio::test]
async fn connection_failures_are_retryable() {
    // Bind and drop, so nothing listens on the port.
    let port = TcpListener::bind("127.0.0.1:0").await.unwrap().local_addr().unwrap().port();
    let f = fetcher("127.0.0.0/8");
    assert_failed(f.fetch(&format!("http://127.0.0.1:{port}/"), 1000).await, true);
}

#[tokio::test]
async fn remote_errors_are_classified() {
    let server = serve(image_server()).await;
    let f = fetcher("127.0.0.0/8");
    assert_failed(f.fetch(&server.url("/404"), 1000).await, false);
    assert_failed(f.fetch(&server.url("/403"), 1000).await, false);
    assert_failed(f.fetch(&server.url("/429"), 1000).await, true);
    assert_failed(f.fetch(&server.url("/500"), 1000).await, true);
    assert_failed(f.fetch(&server.url("/503"), 1000).await, true);
}

#[tokio::test]
async fn only_plain_http_urls_are_fetched() {
    let server = serve(image_server()).await;
    let port = server.addr.port();
    let f = fetcher("127.0.0.0/8");
    for url in [
        "ftp://example.com/a.png".to_owned(),
        "file:///etc/passwd".to_owned(),
        "data:image/png;base64,AAAA".to_owned(),
        format!("http://user:secret@127.0.0.1:{port}/img.png"),
        format!("http://user@127.0.0.1:{port}/img.png"),
        "not a url".to_owned(),
    ] {
        assert_rejected(f.fetch(&url, 1000).await);
    }
    assert_eq!(server.hits(), 0);
}
