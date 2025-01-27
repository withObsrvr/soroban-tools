use clap::{CommandFactory, Parser};
use clap_complete::{generate, Shell};
use std::io;

use crate::Root;

pub const LONG_ABOUT: &str = "\
Print shell completion code for the specified shell

Ensure the completion package for your shell is installed,
e.g., bash-completion for bash.

To enable autocomplete in the current bash shell, run:
  source <(soroban completion --shell bash)

To enable autocomplete permanently, run:
  echo \"source <(soroban completion --shell bash)\" >> ~/.bashrc";

#[derive(Parser, Debug)]
pub struct Cmd {
    /// The shell type
    #[clap(long, arg_enum)]
    shell: Shell,
}

impl Cmd {
    pub fn run(&self) {
        let cmd = &mut Root::command();
        generate(self.shell, cmd, env!("CARGO_BIN_NAME"), &mut io::stdout());
    }
}
