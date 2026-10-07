#!/usr/bin/env bash
#
# Keep the Nitro generation table honest against AWS.
#
# The table in pkg/aws/nitro.go is hand-maintained because it has to be: EC2's
# Hypervisor field says "nitro" or "xen" and carries no VERSION, which AWS
# publishes only in documentation. This script is the answer to the obvious
# objection — how does a hand-maintained table stay current as new instance
# types and new Nitro cards ship?
#
# Two gates, and the second is the one that matters:
#
#   CONTRADICTION  Every family in the table must be reported as "nitro" by
#                  DescribeInstanceTypes. This validates the one dimension the
#                  API *can* validate, automatically. A wrong entry in the
#                  is-it-Nitro axis cannot survive.
#
#   COVERAGE       Every family EC2 currently offers with Hypervisor=nitro must
#                  appear in the table. This is the NEW NITRO CARD DETECTOR: a
#                  new card always arrives carried by new instance families, so
#                  an unclassified family is exactly the signal to go read the
#                  docs. It cannot tell you the new card is v7 — nothing can,
#                  from the API — but it fires precisely when someone needs to
#                  look, which is the difference between a stale table and a
#                  table that tells you it is stale.
#
# What it deliberately does NOT do: guess a version for an unclassified family.
# A fabricated version is indistinguishable from a real one downstream, which is
# the objection truffle#114 raised about prices and truffle#175 reaffirmed.
#
# Usage:
#   AWS_PROFILE=... scripts/nitro-census.sh [region]
set -uo pipefail

REGION="${1:-${AWS_REGION:-us-east-1}}"
root="$(cd "$(dirname "$0")/.." && pwd)"

command -v aws >/dev/null 2>&1 || { echo "aws CLI not found" >&2; exit 2; }

echo "Nitro census against ${REGION} (profile ${AWS_PROFILE:-default})" >&2

# The table, as family<TAB>generation.
table=$(cd "$root" && go run ./scripts/nitrodump 2>/dev/null)
if [ -z "$table" ]; then
  echo "could not dump the table — scripts/nitrodump broken?" >&2
  exit 2
fi
table_count=$(printf '%s\n' "$table" | wc -l | tr -d ' ')

# What AWS offers, as family<TAB>hypervisor. Families, not types: the table is
# keyed by family and one family has many sizes.
aws_pairs=$(aws ec2 describe-instance-types --region "$REGION" \
  --query 'InstanceTypes[].[InstanceType,Hypervisor]' --output text 2>/dev/null \
  | awk -F'\t' '{split($1,a,"."); print a[1] "\t" $2}' | sort -u)
if [ -z "$aws_pairs" ]; then
  echo "DescribeInstanceTypes returned nothing for ${REGION} — wrong profile or region?" >&2
  echo "(not treated as a pass: an empty answer must not look like agreement)" >&2
  exit 2
fi

fail=0

# --- Gate 1: contradiction -------------------------------------------------
# A family we classify that AWS says is xen means the entry is simply wrong.
echo "" >&2
echo "Contradiction gate: families we call Nitro that AWS reports as xen" >&2
contradictions=0
while IFS=$'\t' read -r fam gen; do
  [ -n "$fam" ] || continue
  hv=$(printf '%s\n' "$aws_pairs" | awk -F'\t' -v f="$fam" '$1==f {print $2; exit}')
  # Absent from this region is not a contradiction — regional availability
  # varies and the table is global.
  [ -n "$hv" ] || continue
  # "None" is what --output text prints for an ABSENT Hypervisor, which is the
  # normal answer for bare-metal-only families: every Mac type is a dedicated
  # host with no hypervisor at all, so EC2 reports none. That is not a
  # contradiction of "this is Nitro v5" — it is the field not applying. Only an
  # explicit "xen" contradicts the table. Treating absence as disagreement made
  # all nine Mac families fail on this gate's first run.
  [ "$hv" != "None" ] || continue
  if [ "$hv" != "nitro" ]; then
    fail=1
    contradictions=$((contradictions + 1))
    echo "  ❌ ${fam} is classified Nitro v${gen} but AWS reports hypervisor=${hv}" >&2
  fi
done <<< "$table"
[ "$contradictions" -eq 0 ] && echo "  ✅ none — every classified family present here is nitro" >&2

# --- Gate 2: coverage (the new-card detector) ------------------------------
echo "" >&2
echo "Coverage gate: Nitro families AWS offers that the table does not classify" >&2
unclassified=$(
  printf '%s\n' "$aws_pairs" | awk -F'\t' '$2=="nitro" {print $1}' | sort -u > /tmp/nitro-aws-fams.$$
  printf '%s\n' "$table" | cut -f1 | sort -u > /tmp/nitro-table-fams.$$
  comm -23 /tmp/nitro-aws-fams.$$ /tmp/nitro-table-fams.$$
  rm -f /tmp/nitro-aws-fams.$$ /tmp/nitro-table-fams.$$
)
if [ -n "$unclassified" ]; then
  fail=1
  n=$(printf '%s\n' "$unclassified" | wc -l | tr -d ' ')
  echo "  ❌ ${n} unclassified Nitro famil$([ "$n" = 1 ] && echo y || echo ies):" >&2
  printf '%s\n' "$unclassified" | sed 's/^/       /' >&2
  cat >&2 <<'MSG'

       These return NitroGenerationUnknown today, which is honest but useless.
       A new family is also the only signal AWS gives that a new Nitro card may
       exist — look each up in the Hypervisor column of its family page at
       https://docs.aws.amazon.com/ec2/latest/instancetypes/ec2-nitro-instances.html
       and add it to nitroGenerationByFamily, then bump NitroGenerationAsOf.
MSG
else
  echo "  ✅ none — every Nitro family AWS offers here is classified" >&2
fi

echo "" >&2
echo "table: ${table_count} families | ${REGION}: $(printf '%s\n' "$aws_pairs" | awk -F'\t' '$2=="nitro"' | wc -l | tr -d ' ') nitro families offered" >&2

if [ "$fail" = "0" ]; then
  echo "✅ Nitro table agrees with AWS in ${REGION}." >&2
  exit 0
fi
exit 1
