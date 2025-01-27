use crate::config::{locator, secret};

#[derive(thiserror::Error, Debug)]
pub enum Error {
    #[error(transparent)]
    Secret(#[from] secret::Error),

    #[error(transparent)]
    Config(#[from] locator::Error),
}

#[derive(Debug, clap::Args)]
pub struct Cmd {
    /// Name of identity
    pub name: String,

    #[clap(flatten)]
    pub secrets: secret::Args,

    #[clap(flatten)]
    pub config_locator: locator::Args,
}

impl Cmd {
    pub fn run(&self) -> Result<(), Error> {
        Ok(self
            .config_locator
            .write_identity(&self.name, &self.secrets.read_secret()?)?)
    }
}
