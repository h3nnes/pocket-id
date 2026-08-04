<script lang="ts">
	import FormInput from '$lib/components/form/form-input.svelte';
	import { Button } from '$lib/components/ui/button';
	import * as Field from '$lib/components/ui/field';
	import { Input } from '$lib/components/ui/input';
	import * as Select from '$lib/components/ui/select';
	import { m } from '$lib/paraglide/messages';
	import type { ClaimRemappingSourceType, OidcClientClaimRemapping } from '$lib/types/oidc.type';
	import { LucideMinus, LucidePlus } from '@lucide/svelte';
	import type { Snippet } from 'svelte';
	import type { HTMLAttributes } from 'svelte/elements';
	import { z } from 'zod/v4';

	let {
		claimRemappings = $bindable([]),
		errors,
		disabled = false,
		...restProps
	}: HTMLAttributes<HTMLDivElement> & {
		claimRemappings: OidcClientClaimRemapping[];
		errors?: z.core.$ZodIssue[];
		disabled?: boolean;
		children?: Snippet;
	} = $props();

	// The user-field allowlist mirrors the backend server-side allowlist so the UI never offers a field the API would reject
	const userFields: { value: string; label: string }[] = [
		{ value: 'email', label: m.email() },
		{ value: 'first_name', label: m.first_name() },
		{ value: 'last_name', label: m.last_name() },
		{ value: 'display_name', label: m.display_name() },
		{ value: 'username', label: m.username() },
		{ value: 'locale', label: m.locale() }
	];

	const sourceTypes: { value: ClaimRemappingSourceType; label: string }[] = [
		{ value: 'user_field', label: m.user_field() },
		{ value: 'custom_claim', label: m.custom_claim() },
		{ value: 'static', label: m.static_value() }
	];

	function addClaimRemapping() {
		claimRemappings = [
			...claimRemappings,
			{ claimName: '', sourceType: 'user_field', sourceValue: '' }
		];
	}

	function removeClaimRemapping(index: number) {
		claimRemappings = claimRemappings.filter((_, i) => i !== index);
	}

	function updateClaimRemapping<K extends keyof OidcClientClaimRemapping>(
		index: number,
		field: K,
		value: OidcClientClaimRemapping[K]
	) {
		// Reassign the array so Svelte's reactivity fires on the parent binding even when the state proxy is one level up
		claimRemappings = claimRemappings.map((r, i) => (i === index ? { ...r, [field]: value } : r));
	}

	function getFieldError(index: number, field: keyof OidcClientClaimRemapping): string | null {
		if (!errors) return null;
		const path = [index, field];
		return errors?.filter((e) => e.path[0] == path[0] && e.path[1] == path[1])[0]?.message ?? null;
	}

	function sourceValuePlaceholder(sourceType: ClaimRemappingSourceType): string {
		if (sourceType === 'custom_claim') return 'work_email';
		if (sourceType === 'static') return 'engineering (or a JSON literal like ["a","b"])';
		return '';
	}
</script>

<div {...restProps}>
	<FormInput {disabled}>
		<div class="flex flex-col gap-4">
			{#each claimRemappings as remapping, i (i)}
				<div class="flex flex-col gap-3">
					<div class="flex items-center justify-between">
						<Field.Label>{m.remapping_number({ number: i + 1 })}</Field.Label>
						<Button
							variant="outline"
							size="sm"
							onclick={() => removeClaimRemapping(i)}
							aria-label={m.remove_claim_remapping()}
							{disabled}
						>
							<LucideMinus data-icon="inline-start" />
						</Button>
					</div>

					<div class="grid grid-cols-1 gap-5 md:grid-cols-3">
						<Field.Field>
							<Field.Label required for="claim-name-{i}">{m.claim_name()}</Field.Label>
							<Input
								id="claim-name-{i}"
								placeholder="email"
								value={remapping.claimName}
								oninput={(e) => updateClaimRemapping(i, 'claimName', e.currentTarget.value)}
								aria-invalid={!!getFieldError(i, 'claimName')}
								{disabled}
							/>
							{#if getFieldError(i, 'claimName')}
								<Field.Error>{getFieldError(i, 'claimName')}</Field.Error>
							{/if}
						</Field.Field>

						<Field.Field>
							<Field.Label required for="source-type-{i}">{m.source_type()}</Field.Label>
							<Select.Root
								type="single"
								value={remapping.sourceType}
								onValueChange={(v) => {
									if (!v) return;
									updateClaimRemapping(i, 'sourceType', v as ClaimRemappingSourceType);
									// Reset the source value when the type changes so the UI does not carry a stale value across incompatible inputs
									updateClaimRemapping(i, 'sourceValue', '');
								}}
							>
								<Select.Trigger id="source-type-{i}" {disabled}>
									{sourceTypes.find((s) => s.value === remapping.sourceType)?.label ??
										remapping.sourceType}
								</Select.Trigger>
								<Select.Content>
									{#each sourceTypes as st (st.value)}
										<Select.Item value={st.value}>{st.label}</Select.Item>
									{/each}
								</Select.Content>
							</Select.Root>
						</Field.Field>

						<Field.Field>
							<Field.Label required for="source-value-{i}">{m.source_value()}</Field.Label>
							{#if remapping.sourceType === 'user_field'}
								<Select.Root
									type="single"
									value={remapping.sourceValue}
									onValueChange={(v) => {
										if (v) updateClaimRemapping(i, 'sourceValue', v);
									}}
								>
									<Select.Trigger id="source-value-{i}" {disabled}>
										{userFields.find((f) => f.value === remapping.sourceValue)?.label ??
											m.select_a_field()}
									</Select.Trigger>
									<Select.Content>
										{#each userFields as f (f.value)}
											<Select.Item value={f.value}>{f.label}</Select.Item>
										{/each}
									</Select.Content>
								</Select.Root>
							{:else}
								<Input
									id="source-value-{i}"
									placeholder={sourceValuePlaceholder(remapping.sourceType)}
									value={remapping.sourceValue}
									oninput={(e) => updateClaimRemapping(i, 'sourceValue', e.currentTarget.value)}
									aria-invalid={!!getFieldError(i, 'sourceValue')}
									{disabled}
								/>
							{/if}
							{#if getFieldError(i, 'sourceValue')}
								<Field.Error>{getFieldError(i, 'sourceValue')}</Field.Error>
							{/if}
						</Field.Field>
					</div>
				</div>
			{/each}
		</div>
	</FormInput>

	<Button
		class="mt-7"
		variant="secondary"
		size="sm"
		onclick={addClaimRemapping}
		type="button"
		{disabled}
	>
		<LucidePlus data-icon="inline-start" />
		{claimRemappings.length === 0 ? m.add_claim_remapping() : m.add_another_claim_remapping()}
	</Button>
</div>
