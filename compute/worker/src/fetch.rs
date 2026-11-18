//! Fetches a source given as a URL, without letting the URL reach anything
//! but the public internet.
//!
//! - Only `http` and `https`, without user info. No proxy (the environment's
//!   proxy variables are ignored), no cookies, no credentials of any kind.
//! - Every address is checked by [`FetchPolicy::allows`] before it is used:
//!   host names in the resolver the HTTP client connects with (so the client
//!   can only connect to addresses that passed the check, and DNS rebinding
//!   between check and connect is impossible), IP literals before the
//!   request. An address passes when it is a globally routable unicast
//!   address ([`is_public`]) or lies in a network the deployment allows
//!   (`WORKER_FETCH_ALLOW_CIDRS`).
//! - Redirects are followed by hand, at most [`MAX_REDIRECTS`], each hop
//!   checked like the first.
//! - The body is counted while it is read and abandoned past the size limit;
//!   `Content-Length` is only used to refuse early.
//!
//! The worker's own object storage client is a separate client and is not
//! subject to any of this.

use crate::error::ComputeError;
use bytes::Bytes;
use reqwest::Url;
use reqwest::dns::{Addrs, Name, Resolve, Resolving};
use reqwest::header::{CONTENT_TYPE, LOCATION};
use std::fmt;
use std::future::Future;
use std::net::{IpAddr, Ipv4Addr, Ipv6Addr, SocketAddr};
use std::pin::Pin;
use std::sync::Arc;
use std::time::Duration;

/// Redirects followed before a URL is rejected.
pub const MAX_REDIRECTS: usize = 3;
const USER_AGENT: &str = concat!("lightyear-worker/", env!("CARGO_PKG_VERSION"));

/// Whether `ip` is a globally routable unicast address. Written as the
/// conditions an address must meet, so anything not known to be public —
/// including ranges assigned in the future — is refused:
///
/// - IPv4: unicast (`1.0.0.0`–`223.255.255.255`, so no `0.0.0.0/8`,
///   multicast, `240.0.0.0/4` or broadcast) and outside the IANA
///   special-purpose blocks that are not globally reachable. The global IPv4
///   space has no single prefix, so those blocks are the only list here.
/// - IPv6: inside the global unicast prefix `2000::/3` and outside its
///   special-purpose blocks. This alone refuses loopback, unspecified,
///   IPv4-mapped and -compatible addresses, NAT64 (`64:ff9b::/96`), discard
///   (`100::/64`), unique local, link-local, site-local and multicast.
pub fn is_public(ip: IpAddr) -> bool {
    match ip {
        IpAddr::V4(ip) => is_public_v4(ip),
        IpAddr::V6(ip) => is_public_v6(ip),
    }
}

fn is_public_v4(ip: Ipv4Addr) -> bool {
    /// Special-purpose blocks inside the unicast range that are not globally
    /// reachable (RFC 6890 registry), plus the deprecated 6to4 relay anycast.
    const NOT_GLOBAL: [(Ipv4Addr, u8); 12] = [
        (Ipv4Addr::new(10, 0, 0, 0), 8),      // private
        (Ipv4Addr::new(100, 64, 0, 0), 10),   // shared address space (CGNAT)
        (Ipv4Addr::new(127, 0, 0, 0), 8),     // loopback
        (Ipv4Addr::new(169, 254, 0, 0), 16),  // link-local
        (Ipv4Addr::new(172, 16, 0, 0), 12),   // private
        (Ipv4Addr::new(192, 0, 0, 0), 24),    // IETF protocol assignments
        (Ipv4Addr::new(192, 0, 2, 0), 24),    // TEST-NET-1
        (Ipv4Addr::new(192, 88, 99, 0), 24),  // 6to4 relay anycast
        (Ipv4Addr::new(192, 168, 0, 0), 16),  // private
        (Ipv4Addr::new(198, 18, 0, 0), 15),   // benchmarking
        (Ipv4Addr::new(198, 51, 100, 0), 24), // TEST-NET-2
        (Ipv4Addr::new(203, 0, 113, 0), 24),  // TEST-NET-3
    ];
    let unicast = (1..=223).contains(&ip.octets()[0]);
    unicast
        && !NOT_GLOBAL
            .iter()
            .any(|&(net, len)| Cidr::new(IpAddr::V4(net), len).contains(IpAddr::V4(ip)))
}

fn is_public_v6(ip: Ipv6Addr) -> bool {
    const GLOBAL_UNICAST: (Ipv6Addr, u8) = (Ipv6Addr::new(0x2000, 0, 0, 0, 0, 0, 0, 0), 3);
    /// Special-purpose blocks inside `2000::/3` that are refused.
    const NOT_GLOBAL: [(Ipv6Addr, u8); 4] = [
        // IETF protocol assignments, including Teredo (2001::/32), which
        // embeds an IPv4 address, and benchmarking. A few sub-blocks are
        // global; refusing the whole block is the safe side.
        (Ipv6Addr::new(0x2001, 0, 0, 0, 0, 0, 0, 0), 23),
        (Ipv6Addr::new(0x2001, 0xdb8, 0, 0, 0, 0, 0, 0), 32), // documentation
        (Ipv6Addr::new(0x2002, 0, 0, 0, 0, 0, 0, 0), 16),     // 6to4, embeds an IPv4 address
        (Ipv6Addr::new(0x3fff, 0, 0, 0, 0, 0, 0, 0), 20),     // documentation
    ];
    let v6 = |(net, len): (Ipv6Addr, u8)| Cidr::new(IpAddr::V6(net), len).contains(IpAddr::V6(ip));
    v6(GLOBAL_UNICAST) && !NOT_GLOBAL.into_iter().any(v6)
}

/// An IP network such as `127.0.0.0/8`; a bare address is a single host.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct Cidr {
    net: IpAddr,
    len: u8,
}

impl Cidr {
    fn new(net: IpAddr, len: u8) -> Self {
        Self { net, len }
    }

    /// Addresses of the other family never match: `::ffff:127.0.0.1` is
    /// not in `127.0.0.0/8`.
    pub fn contains(&self, ip: IpAddr) -> bool {
        match (self.net, ip) {
            (IpAddr::V4(net), IpAddr::V4(ip)) => {
                let mask = u32::MAX.checked_shl(32 - u32::from(self.len)).unwrap_or(0);
                u32::from(net) & mask == u32::from(ip) & mask
            }
            (IpAddr::V6(net), IpAddr::V6(ip)) => {
                let mask = u128::MAX.checked_shl(128 - u32::from(self.len)).unwrap_or(0);
                u128::from(net) & mask == u128::from(ip) & mask
            }
            _ => false,
        }
    }
}

impl std::str::FromStr for Cidr {
    type Err = String;

    fn from_str(s: &str) -> Result<Self, String> {
        let (addr, len) = match s.split_once('/') {
            Some((addr, len)) => (addr, Some(len)),
            None => (s, None),
        };
        let net: IpAddr = addr
            .trim()
            .parse()
            .map_err(|_| format!("{s:?} is not an IP network"))?;
        let max = if net.is_ipv4() { 32 } else { 128 };
        let len = match len {
            Some(len) => len
                .trim()
                .parse::<u8>()
                .ok()
                .filter(|&l| l <= max)
                .ok_or_else(|| format!("{s:?} has an invalid prefix length"))?,
            None => max,
        };
        Ok(Self::new(net, len))
    }
}

impl fmt::Display for Cidr {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "{}/{}", self.net, self.len)
    }
}

/// Which addresses a fetch may connect to.
#[derive(Debug, Clone, Default)]
pub struct FetchPolicy {
    /// Networks allowed in addition to the public internet.
    pub allow: Vec<Cidr>,
}

impl FetchPolicy {
    /// Parses a comma-separated list of networks; empty entries are ignored.
    pub fn parse(allow: &str) -> Result<Self, String> {
        let allow = allow
            .split(',')
            .map(str::trim)
            .filter(|s| !s.is_empty())
            .map(str::parse)
            .collect::<Result<_, _>>()?;
        Ok(Self { allow })
    }

    pub fn allows(&self, ip: IpAddr) -> bool {
        is_public(ip) || self.allow.iter().any(|net| net.contains(ip))
    }
}

pub type LookupFuture = Pin<Box<dyn Future<Output = std::io::Result<Vec<SocketAddr>>> + Send>>;

/// Resolves a host name. [`SystemLookup`] in production; tests substitute
/// their own to make names resolve to chosen addresses.
pub trait Lookup: Send + Sync {
    fn lookup(&self, host: &str) -> LookupFuture;
}

pub struct SystemLookup;

impl Lookup for SystemLookup {
    fn lookup(&self, host: &str) -> LookupFuture {
        let host = host.to_owned();
        Box::pin(async move { Ok(tokio::net::lookup_host((host.as_str(), 0)).await?.collect()) })
    }
}

/// A host or address the policy refuses. Found in a request error's source
/// chain, it turns the failure into `url_rejected`.
#[derive(Debug)]
struct Refused(String);

impl fmt::Display for Refused {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(&self.0)
    }
}

impl std::error::Error for Refused {}

/// The HTTP client's resolver: only addresses the policy allows are handed
/// to the connector.
struct CheckedResolver {
    policy: Arc<FetchPolicy>,
    lookup: Arc<dyn Lookup>,
}

impl Resolve for CheckedResolver {
    fn resolve(&self, name: Name) -> Resolving {
        let policy = self.policy.clone();
        let lookup = self.lookup.clone();
        let host = name.as_str().to_owned();
        Box::pin(async move {
            let found = lookup.lookup(&host).await?;
            let allowed: Vec<SocketAddr> = found
                .iter()
                .copied()
                .filter(|addr| policy.allows(addr.ip()))
                .collect();
            if allowed.is_empty() {
                let refused = found.first().map_or_else(
                    || format!("{host} has no address"),
                    |addr| format!("{host} resolves to {}, which is not a public address", addr.ip()),
                );
                return Err(Box::new(Refused(refused)) as Box<dyn std::error::Error + Send + Sync>);
            }
            Ok(Box::new(allowed.into_iter()) as Addrs)
        })
    }
}

pub struct FetchSettings {
    pub connect_timeout: Duration,
    /// Longest wait for any read: the response head or the next body chunk.
    pub read_timeout: Duration,
}

/// A fetched body and its media type.
#[derive(Debug)]
pub struct Fetched {
    pub bytes: Bytes,
    pub mime: String,
}

pub struct Fetcher {
    client: reqwest::Client,
    policy: Arc<FetchPolicy>,
}

impl Fetcher {
    pub fn new(policy: FetchPolicy, settings: &FetchSettings) -> Result<Self, String> {
        Self::with_lookup(policy, settings, Arc::new(SystemLookup))
    }

    pub fn with_lookup(
        policy: FetchPolicy,
        settings: &FetchSettings,
        lookup: Arc<dyn Lookup>,
    ) -> Result<Self, String> {
        let policy = Arc::new(policy);
        let client = reqwest::Client::builder()
            .dns_resolver(Arc::new(CheckedResolver {
                policy: policy.clone(),
                lookup,
            }))
            .no_proxy()
            .redirect(reqwest::redirect::Policy::none())
            .referer(false)
            .user_agent(USER_AGENT)
            .connect_timeout(settings.connect_timeout)
            .read_timeout(settings.read_timeout)
            .build()
            .map_err(|e| format!("build the fetch client: {e}"))?;
        Ok(Self { client, policy })
    }

    /// Fetches `url`, following up to [`MAX_REDIRECTS`] redirects, and
    /// refuses a body larger than `max_bytes`.
    pub async fn fetch(&self, url: &str, max_bytes: u64) -> Result<Fetched, ComputeError> {
        let mut url = Url::parse(url).map_err(|e| rejected(format!("invalid URL: {e}")))?;
        for hop in 0..=MAX_REDIRECTS {
            self.check(&url)?;
            let response = self.client.get(url.clone()).send().await.map_err(request_error)?;
            let status = response.status();
            if matches!(status.as_u16(), 301 | 302 | 303 | 307 | 308) {
                if hop == MAX_REDIRECTS {
                    return Err(rejected(format!(
                        "more than {MAX_REDIRECTS} redirects"
                    )));
                }
                let location = response
                    .headers()
                    .get(LOCATION)
                    .and_then(|v| v.to_str().ok())
                    .ok_or_else(|| failed(format!("remote returned {status} without a Location"), false))?;
                url = url
                    .join(location)
                    .map_err(|e| rejected(format!("invalid redirect location: {e}")))?;
                continue;
            }
            if !status.is_success() {
                // Client errors repeat on every attempt; a few say "later".
                let retryable = !status.is_client_error() || matches!(status.as_u16(), 408 | 429);
                return Err(failed(format!("remote returned {status}"), retryable));
            }
            return read_body(response, max_bytes).await;
        }
        unreachable!("the loop returns on its last hop")
    }

    /// The checks a URL must pass before a request: scheme, no user info,
    /// and an allowed address when the host is an IP literal (the resolver,
    /// which checks host names, is not consulted for those).
    fn check(&self, url: &Url) -> Result<(), ComputeError> {
        if !matches!(url.scheme(), "http" | "https") {
            return Err(rejected(format!("scheme {:?} is not allowed", url.scheme())));
        }
        if !url.username().is_empty() || url.password().is_some() {
            return Err(rejected("URLs with user info are not allowed".into()));
        }
        let ip = match url.host() {
            Some(url::Host::Ipv4(ip)) => IpAddr::V4(ip),
            Some(url::Host::Ipv6(ip)) => IpAddr::V6(ip),
            Some(url::Host::Domain(_)) => return Ok(()),
            None => return Err(rejected("URL has no host".into())),
        };
        if self.policy.allows(ip) {
            Ok(())
        } else {
            Err(rejected(format!("{ip} is not a public address")))
        }
    }
}

async fn read_body(mut response: reqwest::Response, max_bytes: u64) -> Result<Fetched, ComputeError> {
    let too_large = || rejected(format!("source is larger than the {max_bytes}-byte limit"));
    if response.content_length().is_some_and(|len| len > max_bytes) {
        return Err(too_large());
    }
    let declared = response
        .headers()
        .get(CONTENT_TYPE)
        .and_then(|v| v.to_str().ok())
        .map(|v| v.split(';').next().unwrap_or("").trim().to_ascii_lowercase());
    let mut body = Vec::new();
    while let Some(chunk) = response.chunk().await.map_err(request_error)? {
        if (body.len() + chunk.len()) as u64 > max_bytes {
            return Err(too_large());
        }
        body.extend_from_slice(&chunk);
    }
    if body.is_empty() {
        return Err(failed("remote returned an empty body".into(), false));
    }
    let mime = sniff(&body)
        .map(str::to_owned)
        .or(declared.filter(|m| m.starts_with("image/") && m.len() <= 64))
        .unwrap_or_else(|| "application/octet-stream".into());
    Ok(Fetched {
        bytes: Bytes::from(body),
        mime,
    })
}

/// The media type of an image from its first bytes; the remote's
/// `Content-Type` is used only when this finds nothing.
pub fn sniff(bytes: &[u8]) -> Option<&'static str> {
    let at = |offset: usize, magic: &[u8]| bytes.get(offset..offset + magic.len()) == Some(magic);
    if at(0, b"\xFF\xD8\xFF") {
        Some("image/jpeg")
    } else if at(0, b"\x89PNG\r\n\x1A\n") {
        Some("image/png")
    } else if at(0, b"GIF87a") || at(0, b"GIF89a") {
        Some("image/gif")
    } else if at(0, b"RIFF") && at(8, b"WEBP") {
        Some("image/webp")
    } else if at(0, b"\xFF\x0A") || at(0, b"\0\0\0\x0CJXL \r\n\x87\n") {
        Some("image/jxl")
    } else if at(0, b"II*\0") || at(0, b"MM\0*") {
        Some("image/tiff")
    } else if at(4, b"ftyp") {
        let brand = bytes.get(8..12)?;
        match brand {
            b"avif" | b"avis" => Some("image/avif"),
            b"heic" | b"heix" | b"heim" | b"heis" | b"hevc" | b"hevx" | b"mif1" | b"msf1" => {
                Some("image/heic")
            }
            _ => None,
        }
    } else {
        None
    }
}

fn rejected(message: String) -> ComputeError {
    ComputeError::UrlRejected(message)
}

fn failed(message: String, retryable: bool) -> ComputeError {
    ComputeError::FetchFailed { message, retryable }
}

/// A refused address anywhere in the chain is `url_rejected`; everything
/// else (timeouts, refused or reset connections, DNS failures) may work on
/// another attempt.
fn request_error(err: reqwest::Error) -> ComputeError {
    let mut source: Option<&(dyn std::error::Error + 'static)> = Some(&err);
    while let Some(e) = source {
        if let Some(refused) = e.downcast_ref::<Refused>() {
            return rejected(refused.0.clone());
        }
        source = e.source();
    }
    let kind = if err.is_timeout() {
        "timed out"
    } else if err.is_connect() {
        "could not connect"
    } else {
        "request failed"
    };
    // Without the URL: it may carry a presigned signature.
    let err = err.without_url();
    let mut message = err.to_string();
    let mut source = std::error::Error::source(&err);
    while let Some(e) = source {
        message.push_str(": ");
        message.push_str(&e.to_string());
        source = e.source();
    }
    failed(format!("{kind}: {message}"), true)
}

#[cfg(test)]
mod tests;
