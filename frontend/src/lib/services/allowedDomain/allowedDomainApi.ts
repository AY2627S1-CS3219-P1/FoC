export interface AllowedDomain {
	id: string;
	domain: string;
}

export interface AllowedDomainApi {
	listAllowedDomains(): Promise<AllowedDomain[]>;
	allowDomain(domain: string): Promise<void>;
	removeAllowedDomain(id: string): Promise<void>;
}
