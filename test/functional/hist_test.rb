require 'minitest/autorun'

require 'lib/cmd'
require 'lib/repo'

# This set of tests for the `hist` subcommand does nothing to check the
# validity of the output. We just try to hit as many codepaths as we can to
# check that the program doesn't error out.
class TestHist < Minitest::Test
  MODE_FLAGS = ['', '-f', '-l']
  EMAIL_FLAGS = ['', '-e']
  MERGES_FLAGS = ['', '--merges']

  AUTHOR_FILTER_FLAGS = ['', '--author Bob']
  NAUTHOR_FILTER_FLAGS = ['', '--nauthor Alice']
  SINCE_FILTER_FLAGS = ['', '--since 2024-12-25']
  UNTIL_FILTER_FLAGS = ['', '--until 2025-02-01']

  def test_hist_no_flags
    cmd = GitWho.new(GitWho.built_bin_path, TestRepo.path)
    stdout_s = cmd.run 'hist'
    refute_empty(stdout_s)
  end

  all_flag_combos = GitWho.generate_args_cartesian_product([
    MODE_FLAGS,
    EMAIL_FLAGS,
    MERGES_FLAGS,
  ])
  all_flag_combos.each do |flags|
    test_name = "test_hist_(#{flags.join ','})"
    define_method(test_name) do
      cmd = GitWho.new(GitWho.built_bin_path, TestRepo.path)
      stdout_s = cmd.run 'hist', *flags
      refute_empty(stdout_s)
    end
  end

  all_filter_flag_combos = GitWho.generate_args_cartesian_product([
    AUTHOR_FILTER_FLAGS,
    NAUTHOR_FILTER_FLAGS,
    SINCE_FILTER_FLAGS,
    UNTIL_FILTER_FLAGS,
  ])
  all_filter_flag_combos.each do |flags|
    test_name = "test_hist_filter_(#{flags.join ','})"
    define_method(test_name) do
      cmd = GitWho.new(GitWho.built_bin_path, TestRepo.path)
      stdout_s = cmd.run 'hist', *flags
      refute_empty(stdout_s)
    end
  end
end
