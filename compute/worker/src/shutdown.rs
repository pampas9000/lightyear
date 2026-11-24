//! Graceful shutdown. SIGTERM and SIGINT only raise a flag: the worker stops
//! reading dispatches, and a job already running gets a grace period to
//! finish and report. A second signal ends the grace period at once.
//!
//! The flag is never used to cancel a pending `XREADGROUP`: with `NOACK` a
//! dispatch Redis has handed over is acknowledged even if the reply is never
//! read, so dropping that read could lose it. The worker reads with a short
//! block instead and checks the flag between reads.

use std::time::Duration;
use tokio::sync::watch;

/// How many shutdown signals have arrived.
#[derive(Clone)]
pub struct Shutdown(watch::Receiver<u8>);

/// Raises the flag; the signal listener holds it.
pub struct Trigger(watch::Sender<u8>);

pub fn channel() -> (Trigger, Shutdown) {
    let (tx, rx) = watch::channel(0);
    (Trigger(tx), Shutdown(rx))
}

impl Trigger {
    /// Records one more shutdown signal.
    pub fn signal(&self) {
        self.0.send_modify(|n| *n = n.saturating_add(1));
    }
}

impl Shutdown {
    pub fn requested(&self) -> bool {
        *self.0.borrow() > 0
    }

    async fn signals(&mut self, n: u8) {
        if self.0.wait_for(|got| *got >= n).await.is_err() {
            std::future::pending::<()>().await;
        }
    }

    /// Resolves once shutdown has been requested.
    pub async fn wait(&mut self) {
        self.signals(1).await;
    }

    /// Resolves `grace` after shutdown was requested, or at a second signal.
    pub async fn grace_expired(mut self, grace: Duration) {
        self.wait().await;
        tokio::select! {
            () = tokio::time::sleep(grace) => {}
            () = self.signals(2) => {}
        }
    }
}

/// Listens for SIGTERM and SIGINT for the rest of the process.
pub fn listen() -> std::io::Result<Shutdown> {
    use tokio::signal::unix::{SignalKind, signal};
    let mut term = signal(SignalKind::terminate())?;
    let mut int = signal(SignalKind::interrupt())?;
    let (trigger, shutdown) = channel();
    tokio::spawn(async move {
        let mut first = true;
        loop {
            tokio::select! {
                _ = term.recv() => {}
                _ = int.recv() => {}
            }
            if first {
                eprintln!("Shutdown requested: no new jobs; the running one gets the grace period");
                first = false;
            } else {
                eprintln!("Second shutdown signal: stopping the running job now");
            }
            trigger.signal();
        }
    });
    Ok(shutdown)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[tokio::test(start_paused = true)]
    async fn grace_starts_at_the_signal_and_runs_its_full_length() {
        let (trigger, shutdown) = channel();
        assert!(!shutdown.requested());
        let grace = tokio::spawn(shutdown.clone().grace_expired(Duration::from_secs(30)));
        tokio::time::sleep(Duration::from_secs(3600)).await;
        assert!(!grace.is_finished(), "no grace period before a signal");

        trigger.signal();
        assert!(shutdown.requested());
        tokio::time::sleep(Duration::from_secs(29)).await;
        assert!(!grace.is_finished(), "grace period ended early");
        tokio::time::sleep(Duration::from_secs(2)).await;
        assert!(grace.is_finished(), "grace period did not end");
    }

    #[tokio::test(start_paused = true)]
    async fn a_second_signal_ends_the_grace_period() {
        let (trigger, shutdown) = channel();
        trigger.signal();
        let grace = tokio::spawn(shutdown.grace_expired(Duration::from_secs(30)));
        tokio::time::sleep(Duration::from_secs(1)).await;
        assert!(!grace.is_finished());
        trigger.signal();
        tokio::time::sleep(Duration::from_millis(1)).await;
        assert!(grace.is_finished());
    }
}
